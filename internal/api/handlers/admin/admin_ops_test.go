package admin_test

import (
	"testing"
	"time"

	"net/http"

	"github.com/aarondl/null/v8"
	"github.com/aarondl/sqlboiler/v4/boil"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/api"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/models"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/test"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/test/fixtures"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedPaidOrder creates a paid merch order holding stock, the state fulfilment
// starts from.
func seedPaidOrder(t *testing.T, s *api.Server, reference string) (*models.Order, *models.Payment) {
	t.Helper()

	ctx := t.Context()
	fix := fixtures.Fixtures()

	order := &models.Order{
		Reference:    reference,
		Kind:         models.OrderKindMerch,
		Status:       models.OrderStatusPaid,
		Email:        "buyer@example.com",
		FullName:     "Buyer",
		SubtotalKobo: 1500000,
		ShippingKobo: 250000,
		TotalKobo:    1750000,
	}
	require.NoError(t, order.Insert(ctx, s.DB, boil.Infer()))

	item := &models.OrderItem{
		OrderID:          order.ID,
		ProductVariantID: null.StringFrom(fix.VariantHoodieM.ID),
		TitleSnapshot:    "Cloudadore Hoodie",
		VariantSnapshot:  "M / Navy",
		UnitPriceKobo:    1500000,
		Quantity:         1,
		LineTotalKobo:    1500000,
	}
	require.NoError(t, item.Insert(ctx, s.DB, boil.Infer()))

	// The stock this order is holding as sold.
	variant, err := models.FindProductVariant(ctx, s.DB, fix.VariantHoodieM.ID)
	require.NoError(t, err)
	variant.StockSold = 1
	_, err = variant.Update(ctx, s.DB, boil.Whitelist(models.ProductVariantColumns.StockSold, models.ProductVariantColumns.UpdatedAt))
	require.NoError(t, err)

	paymentRow := &models.Payment{
		Provider:    "mock",
		Status:      models.PaymentStatusSucceeded,
		OrderID:     null.StringFrom(order.ID),
		Reference:   reference,
		AmountKobo:  1750000,
		ExpiresAt:   time.Now().Add(time.Hour),
		SucceededAt: null.TimeFrom(time.Now()),
	}
	require.NoError(t, paymentRow.Insert(ctx, s.DB, boil.Infer()))

	return order, paymentRow
}

func advance(t *testing.T, s *api.Server, orderID string, action string) *http.Response {
	t.Helper()

	return test.PerformRequest(t, s, "POST", "/api/v1/admin/orders/"+orderID+"/"+action,
		test.GenericPayload{}, adminAuth(t)).Result()
}

func TestOrderAdvancesThroughFulfilment(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		order, _ := seedPaidOrder(t, s, "cld_mch_FULFIL1")

		for _, step := range []string{"packed", "shipped", "delivered"} {
			res := advance(t, s, order.ID, step)
			require.Equal(t, http.StatusOK, res.StatusCode, "step %s", step)
		}

		require.NoError(t, order.Reload(t.Context(), s.DB))
		assert.Equal(t, models.OrderStatusDelivered, order.Status)
		assert.True(t, order.ShippedAt.Valid)
		assert.True(t, order.DeliveredAt.Valid)
	})
}

// Steps cannot be skipped: an unguarded "set status" endpoint would let an
// order be marked delivered without ever being packed.
func TestOrderCannotSkipFulfilmentSteps(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		order, _ := seedPaidOrder(t, s, "cld_mch_SKIP1")

		res := advance(t, s, order.ID, "delivered")
		assert.Equal(t, http.StatusConflict, res.StatusCode, "paid -> delivered is not a legal step")

		require.NoError(t, order.Reload(t.Context(), s.DB))
		assert.Equal(t, models.OrderStatusPaid, order.Status)
	})
}

// Two admins working from a stale list must not both advance the same order.
func TestRepeatedAdvanceIsRefused(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		order, _ := seedPaidOrder(t, s, "cld_mch_TWICE1")

		require.Equal(t, http.StatusOK, advance(t, s, order.ID, "packed").StatusCode)
		assert.Equal(t, http.StatusConflict, advance(t, s, order.ID, "packed").StatusCode)
	})
}

// Cancelling returns the stock the order was holding.
func TestCancellingAnOrderRestocks(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		fix := fixtures.Fixtures()
		order, _ := seedPaidOrder(t, s, "cld_mch_CANCEL1")

		before, err := models.FindProductVariant(t.Context(), s.DB, fix.VariantHoodieM.ID)
		require.NoError(t, err)
		require.Equal(t, 1, before.StockSold)

		require.Equal(t, http.StatusOK, advance(t, s, order.ID, "cancelled").StatusCode)

		after, err := models.FindProductVariant(t.Context(), s.DB, fix.VariantHoodieM.ID)
		require.NoError(t, err)
		assert.Equal(t, 0, after.StockSold, "cancelling must return the stock")
	})
}

// Past dispatch it is a refund and a return, not a cancellation.
func TestShippedOrderCannotBeCancelled(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		order, _ := seedPaidOrder(t, s, "cld_mch_SHIPPED1")

		require.Equal(t, http.StatusOK, advance(t, s, order.ID, "packed").StatusCode)
		require.Equal(t, http.StatusOK, advance(t, s, order.ID, "shipped").StatusCode)

		assert.Equal(t, http.StatusConflict, advance(t, s, order.ID, "cancelled").StatusCode)
	})
}

// Every privileged action must leave a trace, or there is no way to reconstruct
// who did what when something goes wrong.
func TestAdvancingAnOrderIsAudited(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		order, _ := seedPaidOrder(t, s, "cld_mch_AUDIT1")

		require.Equal(t, http.StatusOK, advance(t, s, order.ID, "packed").StatusCode)

		entries, err := models.AdminAuditLogs(
			models.AdminAuditLogWhere.SubjectID.EQ(order.ID),
		).All(t.Context(), s.DB)
		require.NoError(t, err)

		require.Len(t, entries, 1)
		assert.Equal(t, "order.packed", entries[0].Action)
		assert.Equal(t, "order", entries[0].SubjectType)
		assert.True(t, entries[0].Before.Valid, "the prior state must be captured")
		// The full address is never stored, only a network prefix.
		if entries[0].SourceIPPrefix.Valid {
			assert.Contains(t, entries[0].SourceIPPrefix.String, "/")
		}
	})
}

func TestRefundRecordsAndTransitions(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		_, paymentRow := seedPaidOrder(t, s, "cld_mch_REFUND1")

		res := test.PerformRequest(t, s, "POST", "/api/v1/admin/payments/"+paymentRow.ID+"/refund",
			test.GenericPayload{"reason": "customer changed their mind"}, adminAuth(t))
		require.Equal(t, http.StatusOK, res.Result().StatusCode)

		var refund types.RefundResponse
		test.ParseResponseAndValidate(t, res, &refund)
		assert.Equal(t, int64(1750000), *refund.AmountKobo, "omitting an amount refunds the remainder")

		require.NoError(t, paymentRow.Reload(t.Context(), s.DB))
		assert.Equal(t, models.PaymentStatusRefunded, paymentRow.Status)
	})
}

// A partial refund must leave the payment refundable for the remainder.
func TestPartialRefundLeavesTheRemainderRefundable(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		_, paymentRow := seedPaidOrder(t, s, "cld_mch_PARTIAL1")

		first := test.PerformRequest(t, s, "POST", "/api/v1/admin/payments/"+paymentRow.ID+"/refund",
			test.GenericPayload{"amountKobo": 750000}, adminAuth(t))
		require.Equal(t, http.StatusOK, first.Result().StatusCode)

		require.NoError(t, paymentRow.Reload(t.Context(), s.DB))
		assert.Equal(t, models.PaymentStatusPartiallyRefunded, paymentRow.Status)
		assert.Equal(t, int64(750000), paymentRow.AmountRefundedKobo)

		second := test.PerformRequest(t, s, "POST", "/api/v1/admin/payments/"+paymentRow.ID+"/refund",
			test.GenericPayload{"amountKobo": 1000000}, adminAuth(t))
		require.Equal(t, http.StatusOK, second.Result().StatusCode)

		require.NoError(t, paymentRow.Reload(t.Context(), s.DB))
		assert.Equal(t, models.PaymentStatusRefunded, paymentRow.Status)
	})
}

// Refunding more than was charged must be refused before the provider is called.
func TestRefundCannotExceedTheCharge(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		_, paymentRow := seedPaidOrder(t, s, "cld_mch_OVER1")

		res := test.PerformRequest(t, s, "POST", "/api/v1/admin/payments/"+paymentRow.ID+"/refund",
			test.GenericPayload{"amountKobo": 99999999}, adminAuth(t))
		assert.Equal(t, http.StatusBadRequest, res.Result().StatusCode)

		require.NoError(t, paymentRow.Reload(t.Context(), s.DB))
		assert.Equal(t, models.PaymentStatusSucceeded, paymentRow.Status)
	})
}

// Restock is explicit. A refund does not imply the goods came back.
func TestRefundDoesNotRestockUnlessAsked(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		fix := fixtures.Fixtures()
		_, paymentRow := seedPaidOrder(t, s, "cld_mch_NORESTOCK1")

		res := test.PerformRequest(t, s, "POST", "/api/v1/admin/payments/"+paymentRow.ID+"/refund",
			test.GenericPayload{"restock": false}, adminAuth(t))
		require.Equal(t, http.StatusOK, res.Result().StatusCode)

		variant, err := models.FindProductVariant(t.Context(), s.DB, fix.VariantHoodieM.ID)
		require.NoError(t, err)
		assert.Equal(t, 1, variant.StockSold, "stock must not return unless the operator said so")
	})
}

func TestRefundWithRestockReturnsStock(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		fix := fixtures.Fixtures()
		_, paymentRow := seedPaidOrder(t, s, "cld_mch_RESTOCK1")

		res := test.PerformRequest(t, s, "POST", "/api/v1/admin/payments/"+paymentRow.ID+"/refund",
			test.GenericPayload{"restock": true}, adminAuth(t))
		require.Equal(t, http.StatusOK, res.Result().StatusCode)

		variant, err := models.FindProductVariant(t.Context(), s.DB, fix.VariantHoodieM.ID)
		require.NoError(t, err)
		assert.Equal(t, 0, variant.StockSold)
	})
}

// The alarm must be empty in a healthy system.
func TestUnprocessedWebhookAlarmIsEmptyWhenHealthy(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		res := test.PerformRequest(t, s, "GET", "/api/v1/admin/webhook-events", nil, adminAuth(t))
		require.Equal(t, http.StatusOK, res.Result().StatusCode)

		var events types.WebhookEventList
		test.ParseResponseAndValidate(t, res, &events)
		assert.Empty(t, events.Items)
	})
}

func TestAdminOpsRequireTheAdminScope(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		for _, path := range []string{"/api/v1/admin/orders", "/api/v1/admin/webhook-events"} {
			res := test.PerformRequest(t, s, "GET", path, nil,
				test.HeadersWithAuth(t, fixtures.User1AccessToken1Plain))
			assert.Equal(t, http.StatusForbidden, res.Result().StatusCode, "path %s", path)
		}
	})
}

// The admin scope alone is not enough to move money back out.
//
// AuthModeSecure re-checks last_authenticated_at, so a token that is merely
// valid -- stolen from a browser, lifted from a log, still inside its 24-hour
// life -- cannot issue a refund. This admin holds the scope and a valid token
// but authenticated a month ago.
func TestRefundRequiresRecentAuthentication(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		_, paymentRow := seedPaidOrder(t, s, "cld_mch_STALE1")

		staleAuth := test.HeadersWithAuth(t, fixtures.UserAdminStaleAccessToken1Plain)

		// The scope check passes: a non-destructive admin route works.
		listing := test.PerformRequest(t, s, "GET", "/api/v1/admin/orders", nil, staleAuth)
		require.Equal(t, http.StatusOK, listing.Result().StatusCode)

		// The refund does not.
		refund := test.PerformRequest(t, s, "POST", "/api/v1/admin/payments/"+paymentRow.ID+"/refund",
			test.GenericPayload{}, staleAuth)
		assert.Equal(t, http.StatusUnauthorized, refund.Result().StatusCode,
			"a stale session must not be able to refund")

		require.NoError(t, paymentRow.Reload(t.Context(), s.DB))
		assert.Equal(t, models.PaymentStatusSucceeded, paymentRow.Status, "nothing was refunded")
	})
}
