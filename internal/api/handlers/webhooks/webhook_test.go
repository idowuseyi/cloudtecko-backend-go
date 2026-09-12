package webhooks_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/aarondl/null/v8"
	"github.com/aarondl/sqlboiler/v4/boil"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/api"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/models"
	paymentmock "github.com/idowuseyi/cloudtecko-backend-go/internal/payment/mock"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/payment/paystack"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	webhookPath = "/webhooks/payments"

	// testAmountKobo is ₦100 -- the amount every seeded payment is for, so a
	// webhook claiming anything else is unambiguously a mismatch.
	testAmountKobo = 10000
)

func mockProvider(t *testing.T, s *api.Server) *paymentmock.Provider {
	t.Helper()

	provider, ok := s.Payment.(*paymentmock.Provider)
	require.True(t, ok, "tests must run against the mock payment provider")

	return provider
}

// seedDonationPayment creates a donation and a pending payment for it, the state
// a checkout leaves behind just before the payer is redirected.
func seedDonationPayment(t *testing.T, s *api.Server, reference string) (*models.Donation, *models.Payment) {
	t.Helper()

	ctx := t.Context()

	const amountKobo = testAmountKobo

	donation := &models.Donation{
		Reference:  reference + "-don",
		Kind:       models.DonationKindOneOff,
		Status:     models.DonationStatusPending,
		Email:      "donor@example.com",
		AmountKobo: amountKobo,
	}
	require.NoError(t, donation.Insert(ctx, s.DB, boil.Infer()))

	paymentRow := &models.Payment{
		Provider:   "mock",
		Status:     models.PaymentStatusPending,
		DonationID: null.StringFrom(donation.ID),
		Reference:  reference,
		AmountKobo: amountKobo,
		ExpiresAt:  time.Now().Add(time.Hour),
	}
	require.NoError(t, paymentRow.Insert(ctx, s.DB, boil.Infer()))

	return donation, paymentRow
}

func chargeBody(reference string, providerID string, amountKobo int64) []byte {
	return []byte(fmt.Sprintf(
		`{"event":"charge.success","data":{"id":"%s","reference":"%s","status":"success","amount":%d,"currency":"NGN"}}`,
		providerID, reference, amountKobo))
}

func postWebhook(t *testing.T, s *api.Server, body []byte, signature string) *http.Response {
	t.Helper()

	// Set(), not a literal map: http.Header stores a literal key verbatim, while
	// Get() canonicalises its lookup ("x-paystack-signature" -> "X-Paystack-
	// Signature") and would find nothing. Real requests are unaffected because
	// net/http canonicalises inbound headers, but a hand-built map is not.
	headers := http.Header{}
	headers.Set(paystack.SignatureHeader, signature)

	return test.PerformRequestWithRawBody(t, s, "POST", webhookPath, bytes.NewReader(body), headers, nil).Result()
}

// An unsigned or wrongly-signed body must never reach the money path.
func TestWebhookRejectsInvalidSignature(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		_, paymentRow := seedDonationPayment(t, s, "ref-badsig")

		body := chargeBody("ref-badsig", "evt-1", 10000)

		assert.Equal(t, http.StatusUnauthorized, postWebhook(t, s, body, "deadbeef").StatusCode)
		assert.Equal(t, http.StatusUnauthorized, postWebhook(t, s, body, "").StatusCode)

		require.NoError(t, paymentRow.Reload(t.Context(), s.DB))
		assert.Equal(t, models.PaymentStatusPending, paymentRow.Status, "an unverified body must change nothing")
	})
}

func TestWebhookSettlesPaymentAndDonation(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		donation, paymentRow := seedDonationPayment(t, s, "ref-ok")

		body := chargeBody("ref-ok", "evt-ok", 10000)
		require.Equal(t, http.StatusOK, postWebhook(t, s, body, mockProvider(t, s).SignForTest(body)).StatusCode)

		require.NoError(t, paymentRow.Reload(t.Context(), s.DB))
		assert.Equal(t, models.PaymentStatusSucceeded, paymentRow.Status)
		assert.True(t, paymentRow.SucceededAt.Valid)
		assert.Equal(t, int64(10000), paymentRow.AmountSettledKobo.Int64)

		require.NoError(t, donation.Reload(t.Context(), s.DB))
		assert.Equal(t, models.DonationStatusCompleted, donation.Status)
	})
}

// The central property. The provider's signature carries no timestamp and no
// nonce, so a captured delivery verifies forever -- replay defence is entirely
// ours. Replaying must be a 200 that changes nothing, not a second settlement.
func TestWebhookReplayIsIdempotent(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		_, paymentRow := seedDonationPayment(t, s, "ref-replay")

		body := chargeBody("ref-replay", "evt-replay", 10000)
		signature := mockProvider(t, s).SignForTest(body)

		for attempt := 1; attempt <= 5; attempt++ {
			res := postWebhook(t, s, body, signature)
			assert.Equal(t, http.StatusOK, res.StatusCode, "replay %d must be accepted", attempt)
		}

		require.NoError(t, paymentRow.Reload(t.Context(), s.DB))
		assert.Equal(t, models.PaymentStatusSucceeded, paymentRow.Status)

		// Exactly one recorded event, and exactly one settlement.
		events, err := models.PaymentWebhookEvents().All(t.Context(), s.DB)
		require.NoError(t, err)
		assert.Len(t, events, 1, "five deliveries of one event must record one row")
	})
}

// Every recorded event must end up processed -- including duplicates. Leaving
// correctly-handled replays unprocessed would bury the one signal that catches a
// genuine "money taken, nothing recorded" failure.
func TestWebhookDuplicatesDoNotPolluteTheUnprocessedAlarm(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		seedDonationPayment(t, s, "ref-alarm")

		body := chargeBody("ref-alarm", "evt-alarm", 10000)
		signature := mockProvider(t, s).SignForTest(body)

		postWebhook(t, s, body, signature)
		postWebhook(t, s, body, signature)
		postWebhook(t, s, body, signature)

		unprocessed, err := models.PaymentWebhookEvents(
			models.PaymentWebhookEventWhere.ProcessedAt.IsNull(),
		).Count(t.Context(), s.DB)
		require.NoError(t, err)
		assert.Equal(t, int64(0), unprocessed, "the alarm must stay quiet for handled duplicates")
	})
}

// A signed body claiming a different amount than we recorded must not fulfil.
// It is parked in a terminal state for investigation rather than erroring,
// because a 500 would lose the evidence AND make the provider retry for 72 hours.
func TestWebhookAmountMismatchDoesNotFulfil(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		donation, paymentRow := seedDonationPayment(t, s, "ref-mismatch")

		// Correctly signed, but claims ₦1,000,000 against a ₦100 payment.
		body := chargeBody("ref-mismatch", "evt-mismatch", 100000000)
		res := postWebhook(t, s, body, mockProvider(t, s).SignForTest(body))
		assert.Equal(t, http.StatusOK, res.StatusCode, "recorded, not retried")

		require.NoError(t, paymentRow.Reload(t.Context(), s.DB))
		assert.Equal(t, models.PaymentStatusAmountMismatch, paymentRow.Status)
		assert.False(t, paymentRow.SucceededAt.Valid)

		require.NoError(t, donation.Reload(t.Context(), s.DB))
		assert.Equal(t, models.DonationStatusPending, donation.Status, "the donation must NOT be completed")
	})
}

func TestWebhookChargeFailedMarksPaymentFailed(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		_, paymentRow := seedDonationPayment(t, s, "ref-failed")

		body := []byte(`{"event":"charge.failed","data":{"id":"evt-failed","reference":"ref-failed","status":"failed","amount":10000,"currency":"NGN"}}`)
		require.Equal(t, http.StatusOK, postWebhook(t, s, body, mockProvider(t, s).SignForTest(body)).StatusCode)

		require.NoError(t, paymentRow.Reload(t.Context(), s.DB))
		assert.Equal(t, models.PaymentStatusFailed, paymentRow.Status)
		// A failed charge releases its hold so an abandoned checkout does not sit
		// on stock until the intent expires.
		assert.True(t, paymentRow.HoldReleasedAt.Valid, "a failed charge must release its reservation")
	})
}

// A settled payment must not be walked backwards by a late failure delivery.
func TestWebhookLateFailureCannotUnsettleASucceededPayment(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		_, paymentRow := seedDonationPayment(t, s, "ref-order")

		success := chargeBody("ref-order", "evt-success", 10000)
		require.Equal(t, http.StatusOK, postWebhook(t, s, success, mockProvider(t, s).SignForTest(success)).StatusCode)

		failure := []byte(`{"event":"charge.failed","data":{"id":"evt-late-fail","reference":"ref-order","status":"failed","amount":10000,"currency":"NGN"}}`)
		require.Equal(t, http.StatusOK, postWebhook(t, s, failure, mockProvider(t, s).SignForTest(failure)).StatusCode)

		require.NoError(t, paymentRow.Reload(t.Context(), s.DB))
		assert.Equal(t, models.PaymentStatusSucceeded, paymentRow.Status,
			"the guarded transition must refuse succeeded -> failed")
	})
}

// An authentic event we do not act on is a 200: a non-2xx would make the
// provider retry it for three days.
func TestWebhookUnsupportedEventIsAcceptedNotRetried(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		body := []byte(`{"event":"customer.identification.failed","data":{"id":"evt-x","reference":"r"}}`)
		assert.Equal(t, http.StatusOK, postWebhook(t, s, body, mockProvider(t, s).SignForTest(body)).StatusCode)
	})
}

// An event naming a payment we have no record of must not 500 -- that would put
// the provider into a 72-hour retry loop over something we can never resolve.
func TestWebhookForUnknownPaymentIsAccepted(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		body := chargeBody("no-such-reference", "evt-unknown", 10000)
		assert.Equal(t, http.StatusOK, postWebhook(t, s, body, mockProvider(t, s).SignForTest(body)).StatusCode)
	})
}

// Two genuinely different events on the same transaction must both be recorded:
// keying only on the reference would swallow the second.
func TestWebhookDistinctEventsOnOneTransactionAreBothRecorded(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		seedDonationPayment(t, s, "ref-two")

		charge := chargeBody("ref-two", "evt-a", 10000)
		refund := []byte(`{"event":"refund.processed","data":{"id":"evt-b","reference":"ref-two","status":"success","amount":10000,"currency":"NGN","amount_refunded":10000}}`)

		require.Equal(t, http.StatusOK, postWebhook(t, s, charge, mockProvider(t, s).SignForTest(charge)).StatusCode)
		require.Equal(t, http.StatusOK, postWebhook(t, s, refund, mockProvider(t, s).SignForTest(refund)).StatusCode)

		events, err := models.PaymentWebhookEvents().All(t.Context(), s.DB)
		require.NoError(t, err)
		assert.Len(t, events, 2)
	})
}

// The stored payload must be the provider's own JSON, so an investigation later
// has the actual evidence rather than our interpretation of it.
func TestWebhookStoresTheProviderPayload(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		seedDonationPayment(t, s, "ref-payload")

		body := chargeBody("ref-payload", "evt-payload", 10000)
		require.Equal(t, http.StatusOK, postWebhook(t, s, body, mockProvider(t, s).SignForTest(body)).StatusCode)

		event, err := models.PaymentWebhookEvents().One(t.Context(), s.DB)
		require.NoError(t, err)

		var stored map[string]interface{}
		require.NoError(t, json.Unmarshal(event.Payload, &stored))
		assert.Equal(t, "charge.success", stored["event"])
	})
}
