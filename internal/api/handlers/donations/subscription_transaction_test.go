package donations_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/idowuseyi/cloudtecko-backend-go/internal/api"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/models"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/payment/paystack"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/test"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/test/fixtures"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// webhookDeadline bounds a delivery that is expected to complete. A subscription
// effect that opens its own transaction does not fail here -- it BLOCKS, waiting
// for a connection the outer transaction is holding -- so the assertion has to be
// a timeout rather than an error.
const webhookDeadline = 15 * time.Second

// postSignedWebhookWithin posts a delivery and fails if it does not return in
// time, instead of hanging the package until `go test` gives up.
//
// Everything that can assert -- signing, header construction -- happens on the
// test's own goroutine. Only the blocking request runs elsewhere, because a
// require failing off the test goroutine is undefined behaviour, not a failure.
func postSignedWebhookWithin(t *testing.T, s *api.Server, body []byte) *http.Response {
	t.Helper()

	headers := http.Header{}
	headers.Set(paystack.SignatureHeader, mockPayments(t, s).SignForTest(body))

	done := make(chan *http.Response, 1)

	go func() {
		done <- test.PerformRequestWithRawBody(t, s, "POST", "/webhooks/payments",
			bytesReader(body), headers, nil).Result()
	}()

	select {
	case res := <-done:
		return res
	case <-time.After(webhookDeadline):
		t.Fatalf("webhook delivery did not complete within %s: the handler is blocked, "+
			"which is what a subscription effect opening a nested transaction looks like "+
			"when the pool has no spare connection", webhookDeadline)

		return nil
	}
}

// Subscription webhook effects must run on the CALLER's transaction.
//
// They used to reach for s.db, and MarkSubscriptionDisabled opened a nested
// db.WithTransaction on top of that. A nested transaction needs a second pooled
// connection while the outer one still holds the first, so under contention the
// webhook handler waits for a connection that cannot be freed until it returns.
//
// Pinning the pool to a single connection turns that from "deadlocks when busy"
// into "deadlocks every time", which is the only way to assert it deterministically.
func TestSubscriptionWebhookEffectsRunInTheCallerTransaction(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		fix := fixtures.Fixtures()

		checkout := startRecurring(t, s, fix.DonationTierMonthly.ID, "pool@example.com")

		create := []byte(fmt.Sprintf(
			`{"event":"subscription.create","data":{"id":"evt-pool-c","reference":"%s","status":"success","amount":500000,"currency":"NGN","subscription_code":"SUB_pool","email_token":"tok"}}`,
			*checkout.Reference))
		require.Equal(t, http.StatusOK, postSignedWebhook(t, s, create).StatusCode)

		// One connection: any effect that asks the pool for another blocks forever.
		s.DB.SetMaxOpenConns(1)
		defer s.DB.SetMaxOpenConns(0)

		disable := []byte(
			`{"event":"subscription.disable","data":{"id":"evt-pool-d","status":"complete","subscription_code":"SUB_pool"}}`)

		res := postSignedWebhookWithin(t, s, disable)
		require.Equal(t, http.StatusOK, res.StatusCode)

		donation, err := models.Donations(
			models.DonationWhere.Reference.EQ(*checkout.Reference),
		).One(t.Context(), s.DB)
		require.NoError(t, err)
		assert.Equal(t, models.DonationStatusCancelled, donation.Status,
			"the provider ending a subscription must cancel the donation")

		subscription, err := models.DonationSubscriptions(
			models.DonationSubscriptionWhere.DonationID.EQ(donation.ID),
		).One(t.Context(), s.DB)
		require.NoError(t, err)
		assert.True(t, subscription.CancelledAt.Valid)
	})
}

// The same property for the create and past-due effects, which read and write
// through the executor rather than opening transactions of their own.
func TestSubscriptionCreateAndPastDueRunOnOneConnection(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		fix := fixtures.Fixtures()

		checkout := startRecurring(t, s, fix.DonationTierMonthly.ID, "pool2@example.com")

		s.DB.SetMaxOpenConns(1)
		defer s.DB.SetMaxOpenConns(0)

		create := []byte(fmt.Sprintf(
			`{"event":"subscription.create","data":{"id":"evt-pool2-c","reference":"%s","status":"success","amount":500000,"currency":"NGN","subscription_code":"SUB_pool2","email_token":"tok"}}`,
			*checkout.Reference))
		require.Equal(t, http.StatusOK, postSignedWebhookWithin(t, s, create).StatusCode)

		failed := []byte(
			`{"event":"invoice.payment_failed","data":{"id":"evt-pool2-f","status":"failed","subscription_code":"SUB_pool2"}}`)
		require.Equal(t, http.StatusOK, postSignedWebhookWithin(t, s, failed).StatusCode)

		donation, err := models.Donations(
			models.DonationWhere.Reference.EQ(*checkout.Reference),
		).One(t.Context(), s.DB)
		require.NoError(t, err)
		assert.Equal(t, models.DonationStatusPastDue, donation.Status,
			"a failed renewal is past due, not cancelled")
	})
}
