package local

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/aarondl/null/v8"
	"github.com/aarondl/sqlboiler/v4/boil"
	"github.com/aarondl/sqlboiler/v4/queries"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/models"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/payment"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util/db"
)

// ProcessWebhook records an authenticated provider event and applies its effect.
//
// The whole thing runs in ONE transaction, deliberately. Committing the
// de-duplication row separately would mean a crash between recording and
// processing leaves the event permanently deduplicated and never retried --
// money taken, nothing recorded, with our own guard disabling the provider's
// retry.
//
// Every effect is an idempotent state TRANSITION, never a bare increment.
// `sold = sold + n` inside a webhook is a money printer unless it is gated by a
// conditional status change in the same transaction, which is what
// transitionStatus provides.
//
// Returns whether this delivery did the work. A duplicate returns false and no
// error: the caller answers 200, because a non-2xx would make the provider
// retry something already handled -- for 72 hours, in Paystack's case.
func (s *Service) ProcessWebhook(ctx context.Context, event payment.WebhookEvent, providerName string) (bool, error) {
	log := util.LogFromContext(ctx)

	var applied bool

	err := db.WithTransaction(ctx, s.db, func(exec boil.ContextExecutor) error {
		payload, err := json.Marshal(json.RawMessage(event.Raw))
		if err != nil {
			payload = []byte(`{}`)
		}

		record := &models.PaymentWebhookEvent{
			Provider:          providerName,
			EventType:         string(event.Type),
			IdempotencyKey:    event.IdempotencyKey,
			ProviderReference: null.NewString(event.Transaction.Reference, event.Transaction.Reference != ""),
			Payload:           payload,
			ReceivedAt:        s.clock.Now(),
		}

		// ON CONFLICT DO NOTHING, not catch-the-error.
		//
		// In PostgreSQL a failed statement aborts the ENTIRE transaction, so
		// inserting and then handling a 23505 leaves every subsequent query in
		// this transaction failing with "current transaction is aborted" -- which
		// turns a duplicate delivery into a 500, and a 500 makes the provider
		// retry the duplicate. The guard against redelivery would itself cause
		// redelivery. A conflict must therefore be a non-event at the SQL level.
		inserted, err := s.insertWebhookEventIfNew(ctx, exec, record)
		if err != nil {
			return err
		}

		if !inserted {
			log.Debug().Str("idempotencyKey", event.IdempotencyKey).Msg("Duplicate webhook delivery")

			// Make sure the original row is marked processed so handled duplicates
			// do not sit in the unprocessed alarm forever.
			if err := s.markWebhookProcessed(ctx, exec, providerName, event.IdempotencyKey); err != nil {
				return err
			}

			applied = false

			return nil
		}

		applied, err = s.applyWebhookEffect(ctx, exec, event)
		if err != nil {
			return err
		}

		record.ProcessedAt = null.TimeFrom(s.clock.Now())
		record.ProcessAttempts = 1

		if _, err := record.Update(ctx, exec, boil.Whitelist(
			models.PaymentWebhookEventColumns.ProcessedAt,
			models.PaymentWebhookEventColumns.ProcessAttempts,
		)); err != nil {
			return fmt.Errorf("failed to mark webhook processed: %w", err)
		}

		return nil
	})
	if err != nil {
		return false, err
	}

	return applied, nil
}

// insertWebhookEventIfNew inserts the event unless its idempotency key is
// already present. Reports whether THIS call inserted it.
func (s *Service) insertWebhookEventIfNew(ctx context.Context, exec boil.ContextExecutor, record *models.PaymentWebhookEvent) (bool, error) {
	var insertedID string

	err := queries.Raw(
		`INSERT INTO payment_webhook_events
		    (provider, event_type, idempotency_key, provider_reference, payload, received_at)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 ON CONFLICT (provider, idempotency_key) DO NOTHING
		 RETURNING id`,
		record.Provider, record.EventType, record.IdempotencyKey,
		record.ProviderReference, record.Payload, record.ReceivedAt,
	).QueryRowContext(ctx, exec).Scan(&insertedID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}

		return false, fmt.Errorf("failed to record webhook event: %w", err)
	}

	record.ID = insertedID

	return true, nil
}

func (s *Service) markWebhookProcessed(ctx context.Context, exec boil.ContextExecutor, providerName string, key string) error {
	existing, err := models.PaymentWebhookEvents(
		models.PaymentWebhookEventWhere.Provider.EQ(providerName),
		models.PaymentWebhookEventWhere.IdempotencyKey.EQ(key),
	).One(ctx, exec)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}

		return fmt.Errorf("failed to load existing webhook event: %w", err)
	}

	if existing.ProcessedAt.Valid {
		return nil
	}

	existing.ProcessedAt = null.TimeFrom(s.clock.Now())
	existing.ProcessAttempts++

	if _, err := existing.Update(ctx, exec, boil.Whitelist(
		models.PaymentWebhookEventColumns.ProcessedAt,
		models.PaymentWebhookEventColumns.ProcessAttempts,
	)); err != nil {
		return fmt.Errorf("failed to mark existing webhook processed: %w", err)
	}

	return nil
}

func (s *Service) applyWebhookEffect(ctx context.Context, exec boil.ContextExecutor, event payment.WebhookEvent) (bool, error) {
	switch event.Type {
	case payment.EventChargeSucceeded:
		return s.applyChargeSucceeded(ctx, exec, event)
	case payment.EventChargeFailed:
		return s.applyChargeFailed(ctx, exec, event)
	case payment.EventSubscriptionCreated:
		// Captures the email token, which the provider returns exactly once. Miss
		// it and the donor can never cancel through our interface.
		if err := s.RecordSubscriptionCreated(ctx, exec, event); err != nil {
			return false, err
		}

		return true, nil

	case payment.EventSubscriptionDisabled:
		if err := s.MarkSubscriptionDisabled(ctx, exec, event.SubscriptionCode); err != nil {
			return false, err
		}

		return true, nil

	case payment.EventInvoiceFailed:
		// Past due, NOT cancelled: the provider retries, and a donor whose card
		// expired has not chosen to stop giving.
		if err := s.MarkSubscriptionPastDue(ctx, exec, event.SubscriptionCode); err != nil {
			return false, err
		}

		return true, nil

	case payment.EventRefundProcessed:
		if err := s.recordRefund(ctx, exec, event); err != nil {
			return false, err
		}

		return true, nil
	default:
		return false, nil
	}
}

// applyChargeSucceeded is the money path.
func (s *Service) applyChargeSucceeded(ctx context.Context, exec boil.ContextExecutor, event payment.WebhookEvent) (bool, error) {
	return s.settleSucceededTransaction(ctx, exec, event.Transaction)
}

// settleSucceededTransaction is THE fulfilment routine, shared by the webhook
// and by verify-on-return.
//
// One routine, two triggers, on purpose. A webhook can be delayed, dropped or
// delivered twice, and a redirect proves nothing by itself -- so the payer
// coming back must be able to drive settlement too, and the two paths must not
// be able to disagree about what settlement means. Everything below is guarded,
// so whichever arrives second is a no-op.
func (s *Service) settleSucceededTransaction(ctx context.Context, exec boil.ContextExecutor, transaction payment.Transaction) (bool, error) {
	log := util.LogFromContext(ctx)
	event := payment.WebhookEvent{Transaction: transaction}

	// Look up by OUR reference. Never trust a reference to identify a payment on
	// its own beyond this: an attacker can complete a genuine payment on their
	// own provider account and present that reference, so the amount check below
	// is what actually binds this event to this payment.
	paymentRow, err := models.Payments(
		models.PaymentWhere.Reference.EQ(event.Transaction.Reference),
	).One(ctx, exec)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			log.Warn().Str("reference", event.Transaction.Reference).
				Msg("Webhook referenced a payment we do not have")

			return false, nil
		}

		return false, fmt.Errorf("failed to load payment: %w", err)
	}

	if event.Transaction.AmountMinor != paymentRow.AmountKobo || !currencyMatches(event.Transaction.Currency, paymentRow.Currency) {
		// A persisted state, not an error return: the discrepancy must survive for
		// investigation, and a 500 would lose it while making the provider retry
		// for three days. Never fulfils.
		log.Error().
			Str("reference", paymentRow.Reference).
			Int64("expectedKobo", paymentRow.AmountKobo).
			Int64("reportedKobo", event.Transaction.AmountMinor).
			Str("expectedCurrency", paymentRow.Currency).
			Str("reportedCurrency", event.Transaction.Currency).
			Msg("Payment amount or currency mismatch, refusing to fulfil")

		if _, err := transitionStatus(ctx, exec, transitionTablePayments, paymentRow.ID,
			[]string{models.PaymentStatusPending, models.PaymentStatusProcessing},
			models.PaymentStatusAmountMismatch); err != nil {
			return false, err
		}

		return false, nil
	}

	// The guard. If this reports false, another delivery already succeeded this
	// payment and everything below has already happened exactly once.
	won, err := transitionStatus(ctx, exec, transitionTablePayments, paymentRow.ID,
		[]string{models.PaymentStatusPending, models.PaymentStatusProcessing},
		models.PaymentStatusSucceeded)
	if err != nil {
		return false, err
	}

	if !won {
		log.Debug().Str("reference", paymentRow.Reference).Msg("Payment already settled, skipping effects")

		return false, nil
	}

	paymentRow.ProviderReference = null.NewString(event.Transaction.ProviderReference, event.Transaction.ProviderReference != "")
	paymentRow.ProviderTransactionID = null.NewString(event.Transaction.ProviderID, event.Transaction.ProviderID != "")
	paymentRow.ProviderAuthorizationCode = null.NewString(event.Transaction.AuthorizationCode, event.Transaction.AuthorizationCode != "")
	paymentRow.Channel = null.NewString(event.Transaction.Channel, event.Transaction.Channel != "")
	paymentRow.AmountSettledKobo = null.Int64From(event.Transaction.AmountMinor)
	paymentRow.SucceededAt = null.TimeFrom(s.clock.Now())

	if _, err := paymentRow.Update(ctx, exec, boil.Whitelist(
		models.PaymentColumns.ProviderReference,
		models.PaymentColumns.ProviderTransactionID,
		models.PaymentColumns.ProviderAuthorizationCode,
		models.PaymentColumns.Channel,
		models.PaymentColumns.AmountSettledKobo,
		models.PaymentColumns.SucceededAt,
		models.PaymentColumns.UpdatedAt,
	)); err != nil {
		return false, fmt.Errorf("failed to record payment details: %w", err)
	}

	if paymentRow.DonationID.Valid {
		if err := s.settleDonation(ctx, exec, paymentRow.DonationID.String); err != nil {
			return false, err
		}
	}

	if paymentRow.OrderID.Valid {
		if _, err := transitionStatus(ctx, exec, transitionTableOrders, paymentRow.OrderID.String,
			[]string{models.OrderStatusPending, models.OrderStatusPaymentFailed},
			models.OrderStatusPaid); err != nil {
			return false, err
		}

		if err := s.fulfilOrderReservations(ctx, exec, paymentRow); err != nil {
			return false, err
		}
	}

	return true, nil
}

// fulfilOrderReservations converts a paid order's holds into sales.
//
// Nothing else does this. Reservation happens at checkout and release happens on
// expiry or refund, but the step in between -- the one that says these units are
// SOLD -- only ever runs here, because only here is the money known to have
// arrived. Without it stock_sold and quantity_sold stay at zero for every paid
// order, and the reservation is never given back either: a refund then
// decrements a sold counter that was never incremented and the units are
// withdrawn from sale permanently.
//
// The hold claim is what makes a late webhook safe. If lazy expiry already
// released these reservations, claimHoldRelease loses and releaseHold is false:
// the units are still sold (the payment is real and captured), but reserved is
// left alone rather than decremented a second time. A registration settled that
// way is stamped oversold_at for a human to resolve -- the seats may exceed
// capacity, and that is strictly better than rejecting a captured payment.
func (s *Service) fulfilOrderReservations(ctx context.Context, exec boil.ContextExecutor, paymentRow *models.Payment) error {
	releaseHold, err := claimHoldRelease(ctx, exec, paymentRow.ID)
	if err != nil {
		return err
	}

	items, err := models.OrderItems(
		models.OrderItemWhere.OrderID.EQ(paymentRow.OrderID.String),
	).All(ctx, exec)
	if err != nil {
		return fmt.Errorf("failed to load order items: %w", err)
	}

	for _, item := range items {
		switch {
		case item.ProductVariantID.Valid:
			if err := confirmStock(ctx, exec, item.ProductVariantID.String, item.Quantity, releaseHold); err != nil {
				return err
			}
		case item.TicketTierID.Valid:
			if err := confirmSeats(ctx, exec, item.TicketTierID.String, item.Quantity, releaseHold); err != nil {
				return err
			}
		}
	}

	return s.confirmOrderRegistrations(ctx, exec, paymentRow.OrderID.String, !releaseHold)
}

// confirmOrderRegistrations moves a paid order's registrations to confirmed.
//
// A paid registration is inserted as "reserved" and stays there until the money
// lands, so this is what actually gives someone a ticket. oversold marks the
// ones settled after their hold had already lapsed: the seat is granted anyway,
// but flagged, because capacity may now be exceeded.
func (s *Service) confirmOrderRegistrations(ctx context.Context, exec boil.ContextExecutor, orderID string, oversold bool) error {
	registrations, err := models.EventRegistrations(
		models.EventRegistrationWhere.OrderID.EQ(null.StringFrom(orderID)),
		models.EventRegistrationWhere.Status.EQ(models.RegistrationStatusReserved),
	).All(ctx, exec)
	if err != nil {
		return fmt.Errorf("failed to load order registrations: %w", err)
	}

	for _, registration := range registrations {
		columns := []string{
			models.EventRegistrationColumns.Status,
			models.EventRegistrationColumns.UpdatedAt,
		}

		registration.Status = models.RegistrationStatusConfirmed

		if oversold && !registration.OversoldAt.Valid {
			registration.OversoldAt = null.TimeFrom(s.clock.Now())
			columns = append(columns, models.EventRegistrationColumns.OversoldAt)
		}

		if _, err := registration.Update(ctx, exec, boil.Whitelist(columns...)); err != nil {
			return fmt.Errorf("failed to confirm registration: %w", err)
		}
	}

	return nil
}

func (s *Service) applyChargeFailed(ctx context.Context, exec boil.ContextExecutor, event payment.WebhookEvent) (bool, error) {
	paymentRow, err := models.Payments(
		models.PaymentWhere.Reference.EQ(event.Transaction.Reference),
	).One(ctx, exec)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}

		return false, fmt.Errorf("failed to load payment: %w", err)
	}

	won, err := transitionStatus(ctx, exec, transitionTablePayments, paymentRow.ID,
		[]string{models.PaymentStatusPending, models.PaymentStatusProcessing},
		models.PaymentStatusFailed)
	if err != nil || !won {
		return false, err
	}

	paymentRow.FailedAt = null.TimeFrom(s.clock.Now())
	paymentRow.FailureReason = null.NewString(event.Transaction.FailureReason, event.Transaction.FailureReason != "")

	if _, err := paymentRow.Update(ctx, exec, boil.Whitelist(
		models.PaymentColumns.FailedAt,
		models.PaymentColumns.FailureReason,
		models.PaymentColumns.UpdatedAt,
	)); err != nil {
		return false, fmt.Errorf("failed to record payment failure: %w", err)
	}

	// The hold is released here so an abandoned checkout does not hold stock
	// until its intent expires. Claimed conditionally: expiry may have got here
	// first, and only the winner may decrement a counter.
	if _, err := claimHoldRelease(ctx, exec, paymentRow.ID); err != nil {
		return false, err
	}

	if paymentRow.OrderID.Valid {
		if _, err := transitionStatus(ctx, exec, transitionTableOrders, paymentRow.OrderID.String,
			[]string{models.OrderStatusPending},
			models.OrderStatusPaymentFailed); err != nil {
			return false, err
		}
	}

	return true, nil
}

func (s *Service) settleDonation(ctx context.Context, exec boil.ContextExecutor, donationID string) error {
	donation, err := models.FindDonation(ctx, exec, donationID)
	if err != nil {
		return fmt.Errorf("failed to load donation: %w", err)
	}

	// A one-off donation is complete once paid; a recurring one becomes active
	// and will produce further payments against the same donation row.
	next := models.DonationStatusCompleted
	if donation.Kind == models.DonationKindRecurring {
		next = models.DonationStatusActive
	}

	donation.Status = next
	if _, err := donation.Update(ctx, exec, boil.Whitelist(
		models.DonationColumns.Status,
		models.DonationColumns.UpdatedAt,
	)); err != nil {
		return fmt.Errorf("failed to settle donation: %w", err)
	}

	return nil
}

// currencyMatches treats an absent provider currency as matching, because some
// event payloads omit it; an explicit mismatch never matches.
func currencyMatches(reported string, expected string) bool {
	return reported == "" || reported == expected
}

// UnprocessedWebhookCount is the operational alarm for "money taken, nothing
// recorded".
func (s *Service) UnprocessedWebhookCount(ctx context.Context) (int64, error) {
	count, err := models.PaymentWebhookEvents(
		models.PaymentWebhookEventWhere.ProcessedAt.IsNull(),
	).Count(ctx, s.db)
	if err != nil {
		return 0, fmt.Errorf("failed to count unprocessed webhook events: %w", err)
	}

	return count, nil
}

// recordRefund applies a refund the provider reports.
//
// Refunds can originate in the provider's OWN dashboard, not just from our admin
// API, so this must handle an event we never initiated. Ignoring it would leave
// our records saying a payment succeeded in full while the money has gone back.
func (s *Service) recordRefund(ctx context.Context, exec boil.ContextExecutor, event payment.WebhookEvent) error {
	log := util.LogFromContext(ctx)

	paymentRow, err := models.Payments(
		models.PaymentWhere.Reference.EQ(event.Transaction.Reference),
	).One(ctx, exec)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			log.Warn().Str("reference", event.Transaction.Reference).
				Msg("Refund event referenced a payment we do not have")

			return nil
		}

		return fmt.Errorf("failed to load payment: %w", err)
	}

	// An event with no amount means "the rest of it", not "the whole charge".
	// Assuming the full charge double-counts against anything already refunded,
	// and addRefundedAmount would then refuse the event entirely -- losing the
	// record of a refund that really happened.
	refunded := event.RefundedMinor
	if refunded <= 0 {
		refunded = paymentRow.AmountKobo - paymentRow.AmountRefundedKobo
	}

	if refunded <= 0 {
		log.Warn().Str("reference", paymentRow.Reference).
			Msg("Refund event carried no amount and nothing remains to refund")

		return nil
	}

	// Accumulate through the guarded UPDATE rather than writing a total computed
	// here. A payment can be refunded more than once -- two partial refunds from
	// the dashboard, or one from the dashboard and one from our admin API -- and
	// the amount on this event is THIS refund, not the running total. The cap
	// lives in the same statement, so an event that would take the total past the
	// charge is refused rather than applied.
	totalRefunded, applied, err := addRefundedAmount(ctx, exec, paymentRow.ID, refunded)
	if err != nil {
		return err
	}

	if !applied {
		log.Error().
			Str("reference", paymentRow.Reference).
			Int64("chargedKobo", paymentRow.AmountKobo).
			Int64("alreadyRefundedKobo", paymentRow.AmountRefundedKobo).
			Int64("refundedKobo", refunded).
			Msg("Refund would exceed the original charge, refusing to apply")

		return nil
	}

	won, err := transitionStatus(ctx, exec, transitionTablePayments, paymentRow.ID,
		[]string{models.PaymentStatusSucceeded, models.PaymentStatusPartiallyRefunded},
		refundedStatusFor(totalRefunded, paymentRow.AmountKobo))
	if err != nil {
		return err
	}

	if !won {
		// The amount is recorded but the status would not move -- the payment is
		// already fully refunded, or never succeeded. Left for an operator; the
		// refund row below still lands, so the trail is complete.
		log.Warn().Str("reference", paymentRow.Reference).
			Msg("Recorded a refund whose status transition did not apply")
	}

	// A refund that came from the provider's dashboard has no local refund row,
	// so one is created for the audit trail. restock stays false: whether stock
	// goes back is a human decision that depends on whether the goods were
	// actually returned.
	refundRow := &models.PaymentRefund{
		PaymentID:        paymentRow.ID,
		AmountKobo:       refunded,
		Reason:           "provider-reported",
		Status:           "processed",
		ProviderRefundID: null.NewString(event.Transaction.ProviderID, event.Transaction.ProviderID != ""),
	}

	if err := refundRow.Insert(ctx, exec, boil.Infer()); err != nil {
		return fmt.Errorf("failed to record refund: %w", err)
	}

	return nil
}
