package donations_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/idowuseyi/cloudtecko-backend-go/internal/api"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/models"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/payment/paystack"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/test"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/test/fixtures"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func startRecurring(t *testing.T, s *api.Server, tierID string, email string) types.DonationCheckoutResponse {
	t.Helper()

	res := test.PerformRequest(t, s, "POST", "/api/v1/donations/recurring", test.GenericPayload{
		"tierId": tierID, "email": email,
	}, nil)
	require.Equal(t, http.StatusCreated, res.Result().StatusCode)

	var checkout types.DonationCheckoutResponse
	test.ParseResponseAndValidate(t, res, &checkout)

	return checkout
}

func postSignedWebhook(t *testing.T, s *api.Server, body []byte) *http.Response {
	t.Helper()

	headers := http.Header{}
	headers.Set(paystack.SignatureHeader, mockPayments(t, s).SignForTest(body))

	return test.PerformRequestWithRawBody(t, s, "POST", "/webhooks/payments", bytesReader(body), headers, nil).Result()
}

// The amount comes from the tier, not the request.
func TestRecurringDonationTakesItsAmountFromTheTier(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		fix := fixtures.Fixtures()

		checkout := startRecurring(t, s, fix.DonationTierMonthly.ID, "monthly@example.com")
		assert.Equal(t, fix.DonationTierMonthly.AmountKobo, *checkout.AmountKobo)

		donation, err := models.Donations(
			models.DonationWhere.Reference.EQ(*checkout.Reference),
		).One(t.Context(), s.DB)
		require.NoError(t, err)
		assert.Equal(t, models.DonationKindRecurring, donation.Kind)
	})
}

// A tier with no interval is a suggested one-off amount, not a plan.
func TestNonRecurringTierIsRefused(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		fix := fixtures.Fixtures()

		res := test.PerformRequest(t, s, "POST", "/api/v1/donations/recurring", test.GenericPayload{
			"tierId": fix.DonationTierOneOff.ID, "email": "x@example.com",
		}, nil)
		assert.Equal(t, http.StatusBadRequest, res.Result().StatusCode)
	})
}

// The email token arrives ONCE, on subscription.create. It must be captured and
// stored encrypted -- lose it and the donor can never cancel.
func TestSubscriptionEmailTokenIsCapturedAndEncrypted(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		fix := fixtures.Fixtures()

		checkout := startRecurring(t, s, fix.DonationTierMonthly.ID, "capture@example.com")

		const emailToken = "the-only-copy-of-this-token"

		body := []byte(fmt.Sprintf(
			`{"event":"subscription.create","data":{"id":"evt-sub","reference":"%s","status":"success","amount":500000,"currency":"NGN","subscription_code":"SUB_abc","email_token":"%s"}}`,
			*checkout.Reference, emailToken))

		require.Equal(t, http.StatusOK, postSignedWebhook(t, s, body).StatusCode)

		donation, err := models.Donations(
			models.DonationWhere.Reference.EQ(*checkout.Reference),
		).One(t.Context(), s.DB)
		require.NoError(t, err)

		subscription, err := models.DonationSubscriptions(
			models.DonationSubscriptionWhere.DonationID.EQ(donation.ID),
		).One(t.Context(), s.DB)
		require.NoError(t, err)

		require.True(t, subscription.ProviderEmailToken.Valid, "the token must be captured")
		assert.NotContains(t, subscription.ProviderEmailToken.String, emailToken,
			"the token must be encrypted at rest, not stored verbatim")
		assert.Equal(t, "SUB_abc", subscription.ProviderSubscriptionCode.String)
	})
}

// Cancellation needs the donor's token, exactly like reading status.
func TestCancelRequiresTheAccessToken(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		fix := fixtures.Fixtures()

		checkout := startRecurring(t, s, fix.DonationTierMonthly.ID, "cancel@example.com")

		headers := http.Header{}
		headers.Set("X-Donation-Token", "11111111-1111-4111-8111-111111111111")

		res := test.PerformRequestWithParams(t, s, "POST",
			"/api/v1/donations/"+*checkout.Reference+"/cancel", nil, headers, nil)
		assert.Equal(t, http.StatusNotFound, res.Result().StatusCode)

		// And with no token at all -- same answer, so this cannot be used to
		// discover which references exist.
		res = test.PerformRequest(t, s, "POST", "/api/v1/donations/"+*checkout.Reference+"/cancel", nil, nil)
		assert.Equal(t, http.StatusNotFound, res.Result().StatusCode)
	})
}

// A donor unsure whether it worked will click again.
func TestCancelIsIdempotent(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		fix := fixtures.Fixtures()

		checkout := startRecurring(t, s, fix.DonationTierMonthly.ID, "twice@example.com")

		headers := http.Header{}
		headers.Set("X-Donation-Token", *checkout.AccessToken)

		path := "/api/v1/donations/" + *checkout.Reference + "/cancel"

		first := test.PerformRequestWithParams(t, s, "POST", path, nil, headers, nil)
		require.Equal(t, http.StatusNoContent, first.Result().StatusCode)

		second := test.PerformRequestWithParams(t, s, "POST", path, nil, headers, nil)
		assert.Equal(t, http.StatusNoContent, second.Result().StatusCode)

		donation, err := models.Donations(
			models.DonationWhere.Reference.EQ(*checkout.Reference),
		).One(t.Context(), s.DB)
		require.NoError(t, err)
		assert.Equal(t, models.DonationStatusCancelled, donation.Status)
	})
}

// A failed renewal is PAST DUE, not cancelled. The provider retries, and a donor
// whose card expired has not chosen to stop giving.
func TestFailedRenewalIsPastDueNotCancelled(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		fix := fixtures.Fixtures()

		checkout := startRecurring(t, s, fix.DonationTierMonthly.ID, "pastdue@example.com")

		create := []byte(fmt.Sprintf(
			`{"event":"subscription.create","data":{"id":"evt-c","reference":"%s","status":"success","amount":500000,"currency":"NGN","subscription_code":"SUB_pd","email_token":"tok"}}`,
			*checkout.Reference))
		require.Equal(t, http.StatusOK, postSignedWebhook(t, s, create).StatusCode)

		failed := []byte(`{"event":"invoice.payment_failed","data":{"id":"evt-f","subscription_code":"SUB_pd","status":"failed","amount":500000,"currency":"NGN"}}`)
		require.Equal(t, http.StatusOK, postSignedWebhook(t, s, failed).StatusCode)

		donation, err := models.Donations(
			models.DonationWhere.Reference.EQ(*checkout.Reference),
		).One(t.Context(), s.DB)
		require.NoError(t, err)

		assert.Equal(t, models.DonationStatusPastDue, donation.Status)
		assert.NotEqual(t, models.DonationStatusCancelled, donation.Status,
			"a temporary card problem must not end recurring revenue")
	})
}

// A refund can originate in the provider's own dashboard, so the webhook must
// handle one we never initiated.
func TestProviderInitiatedRefundIsRecorded(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		checkout := startDonation(t, s)

		settle := []byte(fmt.Sprintf(
			`{"event":"charge.success","data":{"id":"evt-s","reference":"%s","status":"success","amount":10000,"currency":"NGN"}}`,
			*checkout.Reference))
		require.Equal(t, http.StatusOK, postSignedWebhook(t, s, settle).StatusCode)

		refund := []byte(fmt.Sprintf(
			`{"event":"refund.processed","data":{"id":"evt-r","reference":"%s","status":"success","amount":10000,"currency":"NGN","amount_refunded":10000}}`,
			*checkout.Reference))
		require.Equal(t, http.StatusOK, postSignedWebhook(t, s, refund).StatusCode)

		paymentRow, err := models.Payments(
			models.PaymentWhere.Reference.EQ(*checkout.Reference),
		).One(t.Context(), s.DB)
		require.NoError(t, err)

		assert.Equal(t, models.PaymentStatusRefunded, paymentRow.Status)
		assert.Equal(t, int64(10000), paymentRow.AmountRefundedKobo)

		refunds, err := models.PaymentRefunds(
			models.PaymentRefundWhere.PaymentID.EQ(paymentRow.ID),
		).All(t.Context(), s.DB)
		require.NoError(t, err)
		assert.Len(t, refunds, 1, "a provider-initiated refund still needs an audit row")
	})
}

// A refund larger than the charge is a discrepancy to investigate, not to apply
// -- and the CHECK forbids it anyway.
func TestRefundExceedingTheChargeIsRefused(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		checkout := startDonation(t, s)

		settle := []byte(fmt.Sprintf(
			`{"event":"charge.success","data":{"id":"evt-s2","reference":"%s","status":"success","amount":10000,"currency":"NGN"}}`,
			*checkout.Reference))
		require.Equal(t, http.StatusOK, postSignedWebhook(t, s, settle).StatusCode)

		refund := []byte(fmt.Sprintf(
			`{"event":"refund.processed","data":{"id":"evt-r2","reference":"%s","status":"success","amount":10000,"currency":"NGN","amount_refunded":99999999}}`,
			*checkout.Reference))
		require.Equal(t, http.StatusOK, postSignedWebhook(t, s, refund).StatusCode, "recorded, not retried")

		paymentRow, err := models.Payments(
			models.PaymentWhere.Reference.EQ(*checkout.Reference),
		).One(t.Context(), s.DB)
		require.NoError(t, err)

		assert.Equal(t, models.PaymentStatusSucceeded, paymentRow.Status, "an impossible refund is not applied")
		assert.Equal(t, int64(0), paymentRow.AmountRefundedKobo)
	})
}
