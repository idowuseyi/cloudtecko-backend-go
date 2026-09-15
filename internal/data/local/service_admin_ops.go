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
	"github.com/aarondl/sqlboiler/v4/queries/qm"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/api/httperrors"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/data/dto"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/models"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/payment"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util/db"
)

// fulfilmentTransitions is the order lifecycle, as data.
//
// Written down rather than scattered through handlers so the whole machine can
// be read at once -- and so an admin cannot skip a step by calling endpoints out
// of order, which is what an unguarded "set status" endpoint would allow.
var fulfilmentTransitions = map[string][]string{
	models.OrderStatusPacked:    {models.OrderStatusPaid},
	models.OrderStatusShipped:   {models.OrderStatusPacked},
	models.OrderStatusDelivered: {models.OrderStatusShipped},
	// Cancellation is allowed from anything not yet dispatched. Past dispatch it
	// is a refund and a return, not a cancellation.
	models.OrderStatusCancelled: {models.OrderStatusPending, models.OrderStatusPaid, models.OrderStatusPacked},
}

// AdvanceOrder moves an order along its fulfilment path.
func (s *Service) AdvanceOrder(ctx context.Context, request dto.AdvanceOrderRequest) (dto.OrderSummary, error) {
	allowedFrom, known := fulfilmentTransitions[request.To]
	if !known {
		return dto.OrderSummary{}, httperrors.ErrConflictInvalidStateTransition
	}

	var summary dto.OrderSummary

	err := db.WithTransaction(ctx, s.db, func(exec boil.ContextExecutor) error {
		order, err := models.FindOrder(ctx, exec, request.OrderID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return httperrors.ErrNotFoundOrder
			}

			return fmt.Errorf("failed to load order: %w", err)
		}

		before := orderSnapshot(order)

		// Guarded, so two admins acting on a stale list cannot both advance the
		// same order. Losing the race IS an error here, unlike webhook handling:
		// a no-op means the operator acted on state that has since moved.
		won, err := transitionStatus(ctx, exec, transitionTableOrders, order.ID, allowedFrom, request.To)
		if err != nil {
			return err
		}

		if !won {
			return httperrors.ErrConflictInvalidStateTransition
		}

		if request.To == models.OrderStatusCancelled {
			if err := s.restockOrder(ctx, exec, order); err != nil {
				return err
			}
		}

		if err := s.stampOrderTimestamps(ctx, exec, order, request.To, request.TrackingReference); err != nil {
			return err
		}

		if err := s.recordAudit(ctx, exec, dto.AuditEntry{
			ActorUserID: request.ActorUserID,
			ActorEmail:  request.ActorEmail,
			Action:      "order." + request.To,
			SubjectType: "order",
			SubjectID:   order.ID,
			Before:      before,
			After:       orderSnapshot(order),
			SourceIP:    request.SourceIP,
		}); err != nil {
			return err
		}

		summary = orderToSummary(order, request.To)

		return nil
	})
	if err != nil {
		return dto.OrderSummary{}, err
	}

	return summary, nil
}

func (s *Service) stampOrderTimestamps(ctx context.Context, exec boil.ContextExecutor, order *models.Order, to string, tracking null.String) error {
	columns := []string{models.OrderColumns.UpdatedAt}

	switch to {
	case models.OrderStatusShipped:
		order.ShippedAt = null.TimeFrom(s.clock.Now())
		order.TrackingReference = tracking
		columns = append(columns, models.OrderColumns.ShippedAt, models.OrderColumns.TrackingReference)
	case models.OrderStatusDelivered:
		order.DeliveredAt = null.TimeFrom(s.clock.Now())
		columns = append(columns, models.OrderColumns.DeliveredAt)
	case models.OrderStatusCancelled:
		order.CancelledAt = null.TimeFrom(s.clock.Now())
		columns = append(columns, models.OrderColumns.CancelledAt)
	}

	if len(columns) == 1 {
		return nil
	}

	if _, err := order.Update(ctx, exec, boil.Whitelist(columns...)); err != nil {
		return fmt.Errorf("failed to stamp order timestamps: %w", err)
	}

	return nil
}

// restockOrder returns stock and seats to circulation.
//
// Only what was actually taken: sold quantities move back, and the hold is
// claimed first so a concurrent expiry cannot double-release it.
func (s *Service) restockOrder(ctx context.Context, exec boil.ContextExecutor, order *models.Order) error {
	items, err := models.OrderItems(
		models.OrderItemWhere.OrderID.EQ(order.ID),
	).All(ctx, exec)
	if err != nil {
		return fmt.Errorf("failed to load order items: %w", err)
	}

	paid := order.Status == models.OrderStatusPaid || order.Status == models.OrderStatusPacked

	for _, item := range items {
		switch {
		case item.ProductVariantID.Valid:
			if err := restockVariant(ctx, exec, item.ProductVariantID.String, item.Quantity, paid); err != nil {
				return err
			}
		case item.TicketTierID.Valid:
			if err := restockTier(ctx, exec, item.TicketTierID.String, item.Quantity, paid); err != nil {
				return err
			}
		}
	}

	return nil
}

func restockVariant(ctx context.Context, exec boil.ContextExecutor, variantID string, quantity int, paid bool) error {
	statement := `UPDATE product_variants
	                 SET stock_reserved = GREATEST(stock_reserved - $2, 0), updated_at = now()
	               WHERE id = $1`
	if paid {
		statement = `UPDATE product_variants
		                SET stock_sold = GREATEST(stock_sold - $2, 0), updated_at = now()
		              WHERE id = $1`
	}

	if _, err := queries.Raw(statement, variantID, quantity).ExecContext(ctx, exec); err != nil {
		return fmt.Errorf("failed to restock variant: %w", err)
	}

	return nil
}

func restockTier(ctx context.Context, exec boil.ContextExecutor, tierID string, quantity int, paid bool) error {
	statement := `UPDATE event_ticket_tiers
	                 SET quantity_reserved = GREATEST(quantity_reserved - $2, 0), updated_at = now()
	               WHERE id = $1`
	if paid {
		statement = `UPDATE event_ticket_tiers
		                SET quantity_sold = GREATEST(quantity_sold - $2, 0), updated_at = now()
		              WHERE id = $1`
	}

	if _, err := queries.Raw(statement, tierID, quantity).ExecContext(ctx, exec); err != nil {
		return fmt.Errorf("failed to restock tier: %w", err)
	}

	return nil
}

// RefundPayment asks the provider to return money, then records it.
//
// The provider call happens FIRST and outside the transaction. Recording a
// refund we failed to actually make would be worse than the reverse: our books
// would say the customer was repaid when they were not. If the provider succeeds
// and the local write then fails, the refund webhook reconciles it.
func (s *Service) RefundPayment(ctx context.Context, request dto.RefundRequest) (dto.RefundResult, error) {
	log := util.LogFromContext(ctx)

	paymentRow, err := models.FindPayment(ctx, s.db, request.PaymentID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return dto.RefundResult{}, httperrors.ErrNotFoundPayment
		}

		return dto.RefundResult{}, fmt.Errorf("failed to load payment: %w", err)
	}

	if paymentRow.Status != models.PaymentStatusSucceeded && paymentRow.Status != models.PaymentStatusPartiallyRefunded {
		return dto.RefundResult{}, httperrors.ErrConflictNotRefundable
	}

	amount := request.AmountKobo
	if amount <= 0 {
		amount = paymentRow.AmountKobo - paymentRow.AmountRefundedKobo
	}

	if amount <= 0 || paymentRow.AmountRefundedKobo+amount > paymentRow.AmountKobo {
		return dto.RefundResult{}, httperrors.ErrBadRequestRefundAmount
	}

	providerResult, err := s.payment.RefundTransaction(ctx, payment.RefundRequest{
		Reference:   paymentRow.Reference,
		AmountMinor: amount,
		Reason:      request.Reason,
	})
	if err != nil {
		log.Err(err).Str("reference", paymentRow.Reference).Msg("Provider refused the refund")

		return dto.RefundResult{}, httperrors.ErrBadGatewayPaymentProvider
	}

	err = db.WithTransaction(ctx, s.db, func(exec boil.ContextExecutor) error {
		refund := &models.PaymentRefund{
			PaymentID:         paymentRow.ID,
			AmountKobo:        amount,
			Reason:            request.Reason,
			Status:            providerResult.Status,
			ProviderRefundID:  null.NewString(providerResult.ProviderRefundID, providerResult.ProviderRefundID != ""),
			RequestedByUserID: request.ActorUserID,
			Restock:           request.Restock,
		}

		if err := refund.Insert(ctx, exec, boil.Infer()); err != nil {
			return fmt.Errorf("failed to record refund: %w", err)
		}

		// paymentRow was read BEFORE this transaction opened, so its refunded total
		// is a snapshot that another refund may already have moved. Accumulating
		// from it would let two concurrent refunds each add to the same stale zero
		// and lose one. addRefundedAmount adds and caps in one statement instead.
		total, applied, err := addRefundedAmount(ctx, exec, paymentRow.ID, amount)
		if err != nil {
			return err
		}

		if !applied {
			// The money has already left the provider at this point, so this is not
			// a rejection of the refund -- it is a discrepancy. Failing the
			// transaction rolls back the refund row and surfaces it rather than
			// recording a total the CHECK forbids.
			return httperrors.ErrConflictRefundExceedsCharge
		}

		if _, err := transitionStatus(ctx, exec, transitionTablePayments, paymentRow.ID,
			[]string{models.PaymentStatusSucceeded, models.PaymentStatusPartiallyRefunded},
			refundedStatusFor(total, paymentRow.AmountKobo)); err != nil {
			return err
		}

		// Restock is a HUMAN decision, passed through from the request: whether
		// goods come back is not something the system can infer from a refund.
		if request.Restock && paymentRow.OrderID.Valid {
			order, err := models.FindOrder(ctx, exec, paymentRow.OrderID.String)
			if err != nil {
				return fmt.Errorf("failed to load order: %w", err)
			}

			if err := s.restockOrder(ctx, exec, order); err != nil {
				return err
			}
		}

		return s.recordAudit(ctx, exec, dto.AuditEntry{
			ActorUserID: request.ActorUserID,
			ActorEmail:  request.ActorEmail,
			Action:      "payment.refund",
			SubjectType: "payment",
			SubjectID:   paymentRow.ID,
			After:       map[string]any{"amountKobo": amount, "restock": request.Restock, "reason": request.Reason},
			SourceIP:    request.SourceIP,
		})
	})
	if err != nil {
		return dto.RefundResult{}, err
	}

	return dto.RefundResult{
		PaymentID:         paymentRow.ID,
		AmountKobo:        amount,
		TotalRefundedKobo: paymentRow.AmountRefundedKobo,
		Status:            providerResult.Status,
	}, nil
}

// recordAudit writes one entry. A failure here fails the whole operation: an
// unrecorded privileged action is exactly what this table exists to prevent.
func (s *Service) recordAudit(ctx context.Context, exec boil.ContextExecutor, entry dto.AuditEntry) error {
	beforeJSON, err := marshalSnapshot(entry.Before)
	if err != nil {
		return err
	}

	afterJSON, err := marshalSnapshot(entry.After)
	if err != nil {
		return err
	}

	record := &models.AdminAuditLog{
		ActorUserID:    entry.ActorUserID,
		ActorEmail:     entry.ActorEmail,
		Action:         entry.Action,
		SubjectType:    entry.SubjectType,
		SubjectID:      entry.SubjectID,
		Before:         beforeJSON,
		After:          afterJSON,
		SourceIPPrefix: null.NewString(TruncateIP(entry.SourceIP), entry.SourceIP != ""),
	}

	if err := record.Insert(ctx, exec, boil.Infer()); err != nil {
		return fmt.Errorf("failed to record audit entry: %w", err)
	}

	return nil
}

func marshalSnapshot(snapshot map[string]any) (null.JSON, error) {
	if snapshot == nil {
		return null.JSON{}, nil
	}

	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return null.JSON{}, fmt.Errorf("failed to encode audit snapshot: %w", err)
	}

	return null.JSONFrom(encoded), nil
}

func orderSnapshot(order *models.Order) map[string]any {
	return map[string]any{
		"status":    order.Status,
		"totalKobo": order.TotalKobo,
		"reference": order.Reference,
	}
}

func orderToSummary(order *models.Order, status string) dto.OrderSummary {
	return dto.OrderSummary{
		ID:        order.ID,
		Reference: order.Reference,
		Status:    status,
		TotalKobo: order.TotalKobo,
		Email:     order.Email,
		CreatedAt: order.CreatedAt,
	}
}

// ListOrders returns orders for admin review.
func (s *Service) ListOrders(ctx context.Context, status null.String) (dto.OrderSummaryList, error) {
	mods := []qm.QueryMod{qm.OrderBy(models.OrderColumns.CreatedAt + " DESC")}
	if status.Valid && status.String != "" {
		mods = append(mods, models.OrderWhere.Status.EQ(status.String))
	}

	orders, err := models.Orders(mods...).All(ctx, s.db)
	if err != nil {
		return nil, fmt.Errorf("failed to list orders: %w", err)
	}

	result := make(dto.OrderSummaryList, len(orders))
	for i, order := range orders {
		result[i] = orderToSummary(order, order.Status)
	}

	return result, nil
}

// ListUnprocessedWebhooks is the operational alarm for "money taken, nothing
// recorded". An entry here means a provider told us about a payment and we
// failed to act on it.
func (s *Service) ListUnprocessedWebhooks(ctx context.Context) (dto.WebhookEventList, error) {
	events, err := models.PaymentWebhookEvents(
		models.PaymentWebhookEventWhere.ProcessedAt.IsNull(),
		qm.OrderBy(models.PaymentWebhookEventColumns.ReceivedAt),
	).All(ctx, s.db)
	if err != nil {
		return nil, fmt.Errorf("failed to list unprocessed webhook events: %w", err)
	}

	result := make(dto.WebhookEventList, len(events))
	for i, event := range events {
		result[i] = dto.WebhookEventSummary{
			ID:                event.ID,
			Provider:          event.Provider,
			EventType:         event.EventType,
			ProviderReference: event.ProviderReference,
			ReceivedAt:        event.ReceivedAt,
			Attempts:          event.ProcessAttempts,
		}
	}

	return result, nil
}
