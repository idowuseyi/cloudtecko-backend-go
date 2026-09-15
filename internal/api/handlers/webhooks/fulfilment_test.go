package webhooks_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/aarondl/null/v8"
	"github.com/aarondl/sqlboiler/v4/boil"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/api"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/models"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/test"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/test/fixtures"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedReservedOrder creates the state a paid checkout leaves behind: an order
// awaiting payment, a line item, a pending payment with an unreleased hold, and
// the reservation already taken against the variant.
func seedReservedOrder(t *testing.T, s *api.Server, reference string, quantity int) (*models.Order, *models.Payment, *models.ProductVariant, int64) {
	t.Helper()

	ctx := t.Context()
	variant := fixtures.Fixtures().VariantHoodieM

	// order_items_line_total_check pins line_total_kobo to unit_price * quantity,
	// and orders_total_sum_check pins the order total to the sum of its parts, so
	// the seed has to be internally consistent rather than convenient. The
	// payment is for the whole order, which is what the webhook's amount is
	// compared against.
	const unitPriceKobo = testAmountKobo

	lineTotalKobo := int64(unitPriceKobo * quantity)

	order := &models.Order{
		Reference:    reference + "-ord",
		Status:       models.OrderStatusPending,
		Email:        "buyer@example.com",
		FullName:     "A Buyer",
		SubtotalKobo: lineTotalKobo,
		TotalKobo:    lineTotalKobo,
	}
	require.NoError(t, order.Insert(ctx, s.DB, boil.Infer()))

	item := &models.OrderItem{
		OrderID:          order.ID,
		ProductVariantID: null.StringFrom(variant.ID),
		TitleSnapshot:    "Cloudadore Hoodie",
		Quantity:         quantity,
		UnitPriceKobo:    unitPriceKobo,
		LineTotalKobo:    lineTotalKobo,
	}
	require.NoError(t, item.Insert(ctx, s.DB, boil.Infer()))

	paymentRow := &models.Payment{
		Provider:   "mock",
		Status:     models.PaymentStatusPending,
		OrderID:    null.StringFrom(order.ID),
		Reference:  reference,
		AmountKobo: lineTotalKobo,
		ExpiresAt:  time.Now().Add(time.Hour),
	}
	require.NoError(t, paymentRow.Insert(ctx, s.DB, boil.Infer()))

	// Take the reservation the way checkout does.
	fresh, err := models.FindProductVariant(ctx, s.DB, variant.ID)
	require.NoError(t, err)

	fresh.StockReserved += quantity
	_, err = fresh.Update(ctx, s.DB, boil.Whitelist(
		models.ProductVariantColumns.StockReserved,
		models.ProductVariantColumns.UpdatedAt))
	require.NoError(t, err)

	return order, paymentRow, fresh, lineTotalKobo
}

// The gap this closes: settlement moved the ORDER to paid but never touched the
// counters, so stock_sold stayed at zero and the reservation was never released.
// A refund then decremented a sold counter that had never been incremented,
// while the reserved units stayed reserved forever -- stock withdrawn from sale
// by the act of selling it.
func TestWebhookConvertsReservedStockIntoSold(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		const quantity = 2

		order, paymentRow, variant, amountKobo := seedReservedOrder(t, s, "ref-fulfil", quantity)

		beforeSold := variant.StockSold
		beforeReserved := variant.StockReserved

		body := chargeBody("ref-fulfil", "evt-fulfil", amountKobo)
		require.Equal(t, http.StatusOK, postWebhook(t, s, body, mockProvider(t, s).SignForTest(body)).StatusCode)

		require.NoError(t, order.Reload(t.Context(), s.DB))
		assert.Equal(t, models.OrderStatusPaid, order.Status)

		require.NoError(t, variant.Reload(t.Context(), s.DB))
		assert.Equal(t, beforeSold+quantity, variant.StockSold,
			"settlement must record the units as sold")
		assert.Equal(t, beforeReserved-quantity, variant.StockReserved,
			"settlement must give the hold back, or the units are reserved forever")

		require.NoError(t, paymentRow.Reload(t.Context(), s.DB))
		assert.True(t, paymentRow.HoldReleasedAt.Valid,
			"the hold must be claimed so lazy expiry cannot release it a second time")
	})
}

// Replay must not sell the units twice. The counters are increments, not
// idempotent writes, so they are only safe behind the status transition.
func TestWebhookReplayDoesNotSellTwice(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		const quantity = 2

		_, _, variant, amountKobo := seedReservedOrder(t, s, "ref-fulfil-twice", quantity)
		beforeSold := variant.StockSold

		body := chargeBody("ref-fulfil-twice", "evt-fulfil-twice", amountKobo)
		signature := mockProvider(t, s).SignForTest(body)

		for range 3 {
			require.Equal(t, http.StatusOK, postWebhook(t, s, body, signature).StatusCode)
		}

		require.NoError(t, variant.Reload(t.Context(), s.DB))
		assert.Equal(t, beforeSold+quantity, variant.StockSold,
			"three deliveries of one event must sell the units once")
	})
}

// A paid ticket registration sits at "reserved" until the money lands. Nothing
// else moves it, so without settlement doing it the buyer never gets a ticket.
func TestWebhookConfirmsPaidRegistrations(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		ctx := t.Context()
		tier := fixtures.Fixtures().TierPaid

		order := &models.Order{
			Reference:    "ref-ticket-ord",
			Status:       models.OrderStatusPending,
			Email:        "attendee@example.com",
			FullName:     "An Attendee",
			SubtotalKobo: testAmountKobo,
			TotalKobo:    testAmountKobo,
		}
		require.NoError(t, order.Insert(ctx, s.DB, boil.Infer()))

		item := &models.OrderItem{
			OrderID:       order.ID,
			TicketTierID:  null.StringFrom(tier.ID),
			TitleSnapshot: "General Admission",
			Quantity:      1,
			UnitPriceKobo: testAmountKobo,
			LineTotalKobo: testAmountKobo,
		}
		require.NoError(t, item.Insert(ctx, s.DB, boil.Infer()))

		registration := &models.EventRegistration{
			EventID:      tier.EventID,
			TicketTierID: tier.ID,
			OrderID:      null.StringFrom(order.ID),
			Email:        "attendee@example.com",
			FullName:     "An Attendee",
			Quantity:     1,
			Status:       models.RegistrationStatusReserved,
			TicketCode:   "CLD-TEST1-TEST2",
		}
		require.NoError(t, registration.Insert(ctx, s.DB, boil.Infer()))

		paymentRow := &models.Payment{
			Provider:   "mock",
			Status:     models.PaymentStatusPending,
			OrderID:    null.StringFrom(order.ID),
			Reference:  "ref-ticket",
			AmountKobo: testAmountKobo,
			ExpiresAt:  time.Now().Add(time.Hour),
		}
		require.NoError(t, paymentRow.Insert(ctx, s.DB, boil.Infer()))

		body := chargeBody("ref-ticket", "evt-ticket", testAmountKobo)
		require.Equal(t, http.StatusOK, postWebhook(t, s, body, mockProvider(t, s).SignForTest(body)).StatusCode)

		require.NoError(t, registration.Reload(ctx, s.DB))
		assert.Equal(t, models.RegistrationStatusConfirmed, registration.Status,
			"a paid registration must be confirmed when the money arrives")
		assert.False(t, registration.OversoldAt.Valid,
			"a registration settled while its hold was still live is not oversold")
	})
}

// amount_refunded is THIS refund, not the running total -- which is the
// distinction the accumulation bug turned on.
func refundBody(reference string, providerID string, amountKobo int64) []byte {
	return []byte(fmt.Sprintf(
		`{"event":"refund.processed","data":{"id":"%s","reference":"%s","status":"processed","amount_refunded":%d,"currency":"NGN"}}`,
		providerID, reference, amountKobo))
}

// Two partial refunds reported by the provider must ACCUMULATE. The webhook
// path used to assign the event's amount over the running total, so a second
// ₦40 refund against a ₦100 charge left the record saying ₦40 refunded -- and
// the payment still looked refundable for ₦60 that had already gone back.
func TestWebhookRefundsAccumulate(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		_, paymentRow := seedDonationPayment(t, s, "ref-refund-acc")

		charge := chargeBody("ref-refund-acc", "evt-refund-acc", testAmountKobo)
		require.Equal(t, http.StatusOK, postWebhook(t, s, charge, mockProvider(t, s).SignForTest(charge)).StatusCode)

		for i, amount := range []int64{4000, 4000} {
			body := refundBody("ref-refund-acc", fmt.Sprintf("evt-refund-%d", i), amount)
			require.Equal(t, http.StatusOK, postWebhook(t, s, body, mockProvider(t, s).SignForTest(body)).StatusCode)
		}

		require.NoError(t, paymentRow.Reload(t.Context(), s.DB))
		assert.Equal(t, int64(8000), paymentRow.AmountRefundedKobo,
			"two refunds of 4000 against a 10000 charge total 8000, not 4000")
		assert.Equal(t, models.PaymentStatusPartiallyRefunded, paymentRow.Status)
	})
}

// The cap is enforced against the RUNNING total, not against each event alone.
func TestWebhookRefundBeyondTheChargeIsRefused(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		_, paymentRow := seedDonationPayment(t, s, "ref-refund-cap")

		charge := chargeBody("ref-refund-cap", "evt-refund-cap", testAmountKobo)
		require.Equal(t, http.StatusOK, postWebhook(t, s, charge, mockProvider(t, s).SignForTest(charge)).StatusCode)

		first := refundBody("ref-refund-cap", "evt-cap-1", 8000)
		require.Equal(t, http.StatusOK, postWebhook(t, s, first, mockProvider(t, s).SignForTest(first)).StatusCode)

		// 8000 + 8000 would be 16000 against a 10000 charge. Each event on its own
		// is under the charge, so only the running total catches this.
		second := refundBody("ref-refund-cap", "evt-cap-2", 8000)
		require.Equal(t, http.StatusOK, postWebhook(t, s, second, mockProvider(t, s).SignForTest(second)).StatusCode,
			"a refused refund is still a handled delivery, not a retry")

		require.NoError(t, paymentRow.Reload(t.Context(), s.DB))
		assert.Equal(t, int64(8000), paymentRow.AmountRefundedKobo,
			"the second refund must not be applied")

		// The amount alone does not prove the cap held: code that assigns rather
		// than accumulates also leaves 8000 here. The refund ROW is what separates
		// "refused" from "applied over the top of the first".
		refunds, err := models.PaymentRefunds(
			models.PaymentRefundWhere.PaymentID.EQ(paymentRow.ID),
		).Count(t.Context(), s.DB)
		require.NoError(t, err)
		assert.Equal(t, int64(1), refunds,
			"a refund that would exceed the charge must not be recorded at all")
	})
}

// An amount-less refund event means "the remainder", not "the whole charge".
func TestWebhookRefundWithoutAmountRefundsTheRemainder(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		_, paymentRow := seedDonationPayment(t, s, "ref-refund-rest")

		charge := chargeBody("ref-refund-rest", "evt-refund-rest", testAmountKobo)
		require.Equal(t, http.StatusOK, postWebhook(t, s, charge, mockProvider(t, s).SignForTest(charge)).StatusCode)

		partial := refundBody("ref-refund-rest", "evt-rest-1", 3000)
		require.Equal(t, http.StatusOK, postWebhook(t, s, partial, mockProvider(t, s).SignForTest(partial)).StatusCode)

		rest := refundBody("ref-refund-rest", "evt-rest-2", 0)
		require.Equal(t, http.StatusOK, postWebhook(t, s, rest, mockProvider(t, s).SignForTest(rest)).StatusCode)

		require.NoError(t, paymentRow.Reload(t.Context(), s.DB))
		assert.Equal(t, int64(testAmountKobo), paymentRow.AmountRefundedKobo)
		assert.Equal(t, models.PaymentStatusRefunded, paymentRow.Status)
	})
}
