package local

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/aarondl/null/v8"
	"github.com/aarondl/sqlboiler/v4/boil"
	"github.com/aarondl/sqlboiler/v4/queries"
	"github.com/aarondl/sqlboiler/v4/queries/qm"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/api/httperrors"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/data/dto"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/models"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/payment"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util/db"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util/hashing"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util/money"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util/ticket"
)

// reserveSeats takes capacity in ONE statement.
//
// The predicate and the increment must not be separable. Written as a SELECT
// followed by an UPDATE, two concurrent callers both read "2 seats left" and
// both reserve 2. Written like this, the second blocks on the first's row lock
// and then re-evaluates its WHERE clause against the newly committed row --
// PostgreSQL's READ COMMITTED behaviour -- so it matches nothing and reports 0
// rows affected.
//
// That 0 is a 409, not an error. This is also why commerce code must stay at
// READ COMMITTED: at REPEATABLE READ the same statement raises a serialization
// failure instead, and the "0 rows means sold out" contract silently becomes a
// 500.
func reserveSeats(ctx context.Context, exec boil.ContextExecutor, tierID string, quantity int) error {
	result, err := queries.Raw(
		`UPDATE event_ticket_tiers
		    SET quantity_reserved = quantity_reserved + $2,
		        updated_at = now()
		  WHERE id = $1
		    AND quantity_reserved + quantity_sold + $2 <= quantity_total`,
		tierID, quantity,
	).ExecContext(ctx, exec)
	if err != nil {
		return fmt.Errorf("failed to reserve seats: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to read rows affected: %w", err)
	}

	if affected != 1 {
		return httperrors.ErrConflictInsufficientCapacity
	}

	return nil
}

// releaseSeats gives capacity back. Callers MUST have won claimHoldRelease
// first: without that gate, a late webhook and lazy expiry both decrement the
// same hold and drive the counter negative.
func releaseSeats(ctx context.Context, exec boil.ContextExecutor, tierID string, quantity int) error {
	if _, err := queries.Raw(
		`UPDATE event_ticket_tiers
		    SET quantity_reserved = GREATEST(quantity_reserved - $2, 0),
		        updated_at = now()
		  WHERE id = $1`,
		tierID, quantity,
	).ExecContext(ctx, exec); err != nil {
		return fmt.Errorf("failed to release seats: %w", err)
	}

	return nil
}

// confirmSeats moves a hold into a sale.
//
// quantity_sold is allowed to pass quantity_total here. If the hold had already
// expired, reserved is not decremented (GREATEST floors it at zero) and the
// registration is stamped oversold. Refusing instead would mean rejecting a
// payment the provider has already captured.
func confirmSeats(ctx context.Context, exec boil.ContextExecutor, tierID string, quantity int, releaseHold bool) error {
	statement := `UPDATE event_ticket_tiers
	                 SET quantity_sold = quantity_sold + $2,
	                     updated_at = now()
	               WHERE id = $1`
	if releaseHold {
		statement = `UPDATE event_ticket_tiers
		                SET quantity_sold = quantity_sold + $2,
		                    quantity_reserved = GREATEST(quantity_reserved - $2, 0),
		                    updated_at = now()
		              WHERE id = $1`
	}

	if _, err := queries.Raw(statement, tierID, quantity).ExecContext(ctx, exec); err != nil {
		return fmt.Errorf("failed to confirm seats: %w", err)
	}

	return nil
}

// expireStaleHolds releases holds whose payment intent has lapsed.
//
// Called at the TOP of every registration attempt rather than by a scheduler.
// This repository has no job runner, and a design that depends on a cron nobody
// built oversells in week one. Contention on a tier is exactly when expiry
// matters, and contention is exactly when this runs -- so the lazy path is the
// one that must be correct; a cron subcommand is only a backstop for a tier
// nobody is currently buying.
func (s *Service) expireStaleHolds(ctx context.Context, exec boil.ContextExecutor, tierID string) error {
	stale, err := models.Payments(
		models.PaymentWhere.Status.EQ(models.PaymentStatusPending),
		models.PaymentWhere.ExpiresAt.LT(s.clock.Now()),
		models.PaymentWhere.HoldReleasedAt.IsNull(),
		qm.InnerJoin("orders o ON o.id = payments.order_id"),
		qm.InnerJoin("order_items oi ON oi.order_id = o.id"),
		qm.Where("oi.ticket_tier_id = ?", tierID),
	).All(ctx, exec)
	if err != nil {
		return fmt.Errorf("failed to find stale holds: %w", err)
	}

	for _, paymentRow := range stale {
		won, err := claimHoldRelease(ctx, exec, paymentRow.ID)
		if err != nil {
			return err
		}

		if !won {
			continue
		}

		if _, err := transitionStatus(ctx, exec, transitionTablePayments, paymentRow.ID,
			[]string{models.PaymentStatusPending}, models.PaymentStatusExpired); err != nil {
			return err
		}

		items, err := models.OrderItems(
			models.OrderItemWhere.OrderID.EQ(paymentRow.OrderID.String),
			models.OrderItemWhere.TicketTierID.IsNotNull(),
		).All(ctx, exec)
		if err != nil {
			return fmt.Errorf("failed to load expired order items: %w", err)
		}

		for _, item := range items {
			if err := releaseSeats(ctx, exec, item.TicketTierID.String, item.Quantity); err != nil {
				return err
			}
		}

		if _, err := transitionStatus(ctx, exec, transitionTableOrders, paymentRow.OrderID.String,
			[]string{models.OrderStatusPending}, models.OrderStatusExpired); err != nil {
			return err
		}
	}

	return nil
}

// RegisterForEvent books seats on a tier.
//
// A free tier registers immediately and confirms; a paid tier reserves, creates
// an order and a payment intent, and waits for the provider. The caller cannot
// choose which: it follows from the tier's price, so a client cannot ask to skip
// payment.
func (s *Service) RegisterForEvent(ctx context.Context, request dto.EventRegistrationRequest) (dto.EventRegistrationResult, error) {
	log := util.LogFromContext(ctx)

	var result dto.EventRegistrationResult

	err := db.WithTransaction(ctx, s.db, func(exec boil.ContextExecutor) error {
		event, err := models.Events(
			models.EventWhere.Slug.EQ(request.EventSlug),
			models.EventWhere.PublishedAt.IsNotNull(),
			models.EventWhere.ArchivedAt.IsNull(),
		).One(ctx, exec)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return httperrors.ErrNotFoundEvent
			}

			return fmt.Errorf("failed to load event: %w", err)
		}

		tier, err := s.requireTier(ctx, exec, event.ID, request.TierID)
		if err != nil {
			return err
		}

		if err := s.checkSalesWindow(tier); err != nil {
			return err
		}

		if tier.MaxPerOrder > 0 && request.Quantity > tier.MaxPerOrder {
			return httperrors.ErrBadRequestQuantityTooHigh
		}

		// Reclaim lapsed holds before competing for capacity, so an abandoned
		// checkout does not keep a seat out of circulation until a cron notices.
		if err := s.expireStaleHolds(ctx, exec, tier.ID); err != nil {
			return err
		}

		if err := reserveSeats(ctx, exec, tier.ID, request.Quantity); err != nil {
			return err
		}

		ticketCode, err := ticket.New()
		if err != nil {
			return err
		}

		registration := &models.EventRegistration{
			EventID:       event.ID,
			TicketTierID:  tier.ID,
			UserID:        request.UserID,
			Email:         request.Email,
			FullName:      request.FullName,
			Quantity:      request.Quantity,
			UnitPriceKobo: tier.PriceKobo,
			Status:        models.RegistrationStatusReserved,
			TicketCode:    ticketCode,
		}

		if tier.PriceKobo == 0 {
			// Free RSVP: no order, no payment, confirmed immediately. The seats move
			// straight from reserved to sold.
			registration.Status = models.RegistrationStatusConfirmed

			if err := s.insertRegistration(ctx, exec, registration); err != nil {
				return err
			}

			if err := confirmSeats(ctx, exec, tier.ID, request.Quantity, true); err != nil {
				return err
			}

			result = dto.EventRegistrationResult{
				TicketCode: ticketCode,
				Status:     registration.Status,
				AmountKobo: 0,
			}

			return nil
		}

		order, paymentRow, accessToken, err := s.createTicketOrder(ctx, exec, event, tier, registration, request)
		if err != nil {
			return err
		}

		registration.OrderID = null.StringFrom(order.ID)
		if err := s.insertRegistration(ctx, exec, registration); err != nil {
			return err
		}

		result = dto.EventRegistrationResult{
			TicketCode:  ticketCode,
			Status:      registration.Status,
			Reference:   paymentRow.Reference,
			AmountKobo:  order.TotalKobo,
			AccessToken: accessToken,
		}

		return nil
	})
	if err != nil {
		return dto.EventRegistrationResult{}, err
	}

	// Paid registrations need somewhere to pay. As with donations, the provider
	// call happens after the commit so a third party's latency does not pin a
	// database connection.
	if result.Reference != "" {
		initialized, err := s.payment.InitializeTransaction(ctx, payment.InitializeRequest{
			Reference:   result.Reference,
			AmountMinor: result.AmountKobo,
			Currency:    models.CurrencyCodeNGN,
			Email:       request.Email,
			CallbackURL: s.config.Payment.CallbackURL,
			Metadata:    map[string]string{"ticket_code": result.TicketCode},
		})
		if err != nil {
			log.Err(err).Str("reference", result.Reference).Msg("Failed to initialize ticket payment")

			return dto.EventRegistrationResult{}, httperrors.ErrBadGatewayPaymentProvider
		}

		if err := s.recordAuthorizationURL(ctx, result.Reference, initialized.AuthorizationURL); err != nil {
			return dto.EventRegistrationResult{}, err
		}

		result.AuthorizationURL = initialized.AuthorizationURL
	}

	return result, nil
}

func (s *Service) insertRegistration(ctx context.Context, exec boil.ContextExecutor, registration *models.EventRegistration) error {
	if err := registration.Insert(ctx, exec, boil.Infer()); err != nil {
		if db.IsUniqueViolation(err, "idx_event_registrations_one_live_per_email") {
			// A double-clicked RSVP, not a new booking.
			return httperrors.ErrConflictAlreadyRegistered
		}

		return fmt.Errorf("failed to insert registration: %w", err)
	}

	return nil
}

func (s *Service) createTicketOrder(
	ctx context.Context,
	exec boil.ContextExecutor,
	event *models.Event,
	tier *models.EventTicketTier,
	registration *models.EventRegistration,
	request dto.EventRegistrationRequest,
) (*models.Order, *models.Payment, string, error) {
	lineTotal, err := money.LineTotal(tier.PriceKobo, request.Quantity)
	if err != nil {
		return nil, nil, "", fmt.Errorf("failed to compute line total: %w", err)
	}

	// Tickets are not shipped, so shipping is zero and the tax base is the
	// ticket price alone.
	totals, err := money.ComputeTotals(lineTotal, 0, 0, money.BasisPoints(s.config.Payment.TaxRateBasisPoints))
	if err != nil {
		return nil, nil, "", fmt.Errorf("failed to compute totals: %w", err)
	}

	reference, err := newPaymentReference("tkt")
	if err != nil {
		return nil, nil, "", err
	}

	accessTokenPlain, accessTokenHash, err := hashing.NewToken()
	if err != nil {
		return nil, nil, "", fmt.Errorf("failed to generate order access token: %w", err)
	}

	order := &models.Order{
		Reference:            reference,
		Kind:                 models.OrderKindTicket,
		Status:               models.OrderStatusPending,
		UserID:               request.UserID,
		Email:                request.Email,
		FullName:             request.FullName,
		SubtotalKobo:         totals.SubtotalKobo,
		ShippingKobo:         totals.ShippingKobo,
		TaxKobo:              totals.TaxKobo,
		DiscountKobo:         totals.DiscountKobo,
		TotalKobo:            totals.TotalKobo,
		ReservationExpiresAt: null.TimeFrom(s.clock.Now().Add(s.config.Payment.IntentValidity)),
		AccessTokenHash:      null.BytesFrom(accessTokenHash),
	}

	if err := order.Insert(ctx, exec, boil.Infer()); err != nil {
		return nil, nil, "", fmt.Errorf("failed to insert order: %w", err)
	}

	item := &models.OrderItem{
		OrderID:         order.ID,
		TicketTierID:    null.StringFrom(tier.ID),
		TitleSnapshot:   event.Title,
		VariantSnapshot: tier.Name,
		UnitPriceKobo:   tier.PriceKobo,
		Quantity:        request.Quantity,
		LineTotalKobo:   lineTotal,
	}

	if err := item.Insert(ctx, exec, boil.Infer()); err != nil {
		return nil, nil, "", fmt.Errorf("failed to insert order item: %w", err)
	}

	paymentRow := &models.Payment{
		Provider:   s.paymentProviderName,
		Status:     models.PaymentStatusPending,
		OrderID:    null.StringFrom(order.ID),
		Reference:  reference,
		AmountKobo: totals.TotalKobo,
		ExpiresAt:  s.clock.Now().Add(s.config.Payment.IntentValidity),
	}

	if err := paymentRow.Insert(ctx, exec, boil.Infer()); err != nil {
		return nil, nil, "", fmt.Errorf("failed to insert payment: %w", err)
	}

	_ = registration

	return order, paymentRow, accessTokenPlain, nil
}

func (s *Service) requireTier(ctx context.Context, exec boil.ContextExecutor, eventID string, tierID string) (*models.EventTicketTier, error) {
	tier, err := models.EventTicketTiers(
		models.EventTicketTierWhere.ID.EQ(tierID),
		models.EventTicketTierWhere.EventID.EQ(eventID),
		models.EventTicketTierWhere.ArchivedAt.IsNull(),
	).One(ctx, exec)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, httperrors.ErrNotFoundTicketTier
		}

		return nil, fmt.Errorf("failed to load ticket tier: %w", err)
	}

	return tier, nil
}

func (s *Service) checkSalesWindow(tier *models.EventTicketTier) error {
	now := s.clock.Now()

	if tier.SalesStartAt.Valid && now.Before(tier.SalesStartAt.Time) {
		return httperrors.ErrConflictSalesNotOpen
	}

	if tier.SalesEndAt.Valid && now.After(tier.SalesEndAt.Time) {
		return httperrors.ErrConflictSalesClosed
	}

	return nil
}

// TierAvailability reports remaining capacity for the gallery badge.
//
// Derived at read time, never stored: it changes on every sale, so a stored
// copy would be wrong more often than right.
func (s *Service) TierAvailability(ctx context.Context, eventID string) (int, int, error) {
	tiers, err := models.EventTicketTiers(
		models.EventTicketTierWhere.EventID.EQ(eventID),
		models.EventTicketTierWhere.ArchivedAt.IsNull(),
	).All(ctx, s.db)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to load ticket tiers: %w", err)
	}

	var taken, capacity int

	for _, tier := range tiers {
		// Holds count as taken: a seat part-way through payment is not available,
		// and the gallery badge would otherwise advertise it.
		taken += tier.QuantitySold + tier.QuantityReserved
		capacity += tier.QuantityTotal
	}

	return taken, capacity, nil
}

// ListTicketTiers returns an event's tiers with remaining capacity.
//
// Remaining subtracts BOTH sold and reserved: a seat someone is part-way through
// paying for is not available, even though no money has arrived yet. Showing it
// as available is how two people end up buying the last ticket.
func (s *Service) ListTicketTiers(ctx context.Context, eventSlug string) (dto.TicketTierList, error) {
	event, err := models.Events(
		models.EventWhere.Slug.EQ(eventSlug),
		models.EventWhere.PublishedAt.IsNotNull(),
		models.EventWhere.ArchivedAt.IsNull(),
	).One(ctx, s.db)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, httperrors.ErrNotFoundEvent
		}

		return nil, fmt.Errorf("failed to load event: %w", err)
	}

	tiers, err := models.EventTicketTiers(
		models.EventTicketTierWhere.EventID.EQ(event.ID),
		models.EventTicketTierWhere.ArchivedAt.IsNull(),
		qm.OrderBy(models.EventTicketTierColumns.SortOrder+", "+models.EventTicketTierColumns.PriceKobo),
	).All(ctx, s.db)
	if err != nil {
		return nil, fmt.Errorf("failed to load ticket tiers: %w", err)
	}

	result := make(dto.TicketTierList, len(tiers))
	for i, tier := range tiers {
		remaining := tier.QuantityTotal - tier.QuantitySold - tier.QuantityReserved
		if remaining < 0 {
			// Possible after an oversell, and reporting a negative number to the
			// public would be nonsense.
			remaining = 0
		}

		result[i] = dto.TicketTier{
			ID:          tier.ID,
			Name:        tier.Name,
			Description: tier.Description,
			PriceKobo:   tier.PriceKobo,
			Currency:    tier.Currency,
			Remaining:   remaining,
			MaxPerOrder: tier.MaxPerOrder,
		}
	}

	return result, nil
}
