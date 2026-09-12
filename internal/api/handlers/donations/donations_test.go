package donations_test

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/aarondl/sqlboiler/v4/queries/qm"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/api"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/models"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/payment"
	paymentmock "github.com/idowuseyi/cloudtecko-backend-go/internal/payment/mock"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/payment/paystack"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/test"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/types"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util/hashing"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// oneHundredNaira, in kobo. Money is always integer minor units.
const oneHundredNaira = 10000

func mockPayments(t *testing.T, s *api.Server) *paymentmock.Provider {
	t.Helper()

	provider, ok := s.Payment.(*paymentmock.Provider)
	require.True(t, ok, "tests must run against the mock payment provider")

	return provider
}

func startDonation(t *testing.T, s *api.Server) types.DonationCheckoutResponse {
	t.Helper()

	res := test.PerformRequest(t, s, "POST", "/api/v1/donations/checkout", test.GenericPayload{
		"email":      "donor@example.com",
		"amountKobo": oneHundredNaira,
	}, nil)
	require.Equal(t, http.StatusCreated, res.Result().StatusCode)

	var checkout types.DonationCheckoutResponse
	test.ParseResponseAndValidate(t, res, &checkout)

	return checkout
}

func readStatus(t *testing.T, s *api.Server, reference string, token string) *http.Response {
	t.Helper()

	headers := http.Header{}
	headers.Set("X-Donation-Token", token)

	return test.PerformRequestWithParams(t, s, "GET", "/api/v1/donations/"+reference, nil, headers, nil).Result()
}

func TestDonationCheckoutCreatesPendingIntent(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		checkout := startDonation(t, s)

		assert.NotEmpty(t, *checkout.AuthorizationURL)
		assert.Equal(t, int64(oneHundredNaira), *checkout.AmountKobo)
		assert.Contains(t, *checkout.Reference, "cld_don_")

		donation, err := models.Donations(
			models.DonationWhere.Reference.EQ(*checkout.Reference),
		).One(t.Context(), s.DB)
		require.NoError(t, err)

		assert.Equal(t, models.DonationStatusPending, donation.Status)
		assert.Equal(t, int64(oneHundredNaira), donation.AmountKobo)

		paymentRow, err := models.Payments(
			models.PaymentWhere.Reference.EQ(*checkout.Reference),
		).One(t.Context(), s.DB)
		require.NoError(t, err)
		assert.Equal(t, models.PaymentStatusPending, paymentRow.Status)
	})
}

// Only the hash is stored. The plaintext exists once, in the response.
func TestDonationAccessTokenIsStoredHashed(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		checkout := startDonation(t, s)

		donation, err := models.Donations(
			models.DonationWhere.Reference.EQ(*checkout.Reference),
		).One(t.Context(), s.DB)
		require.NoError(t, err)

		require.True(t, donation.AccessTokenHash.Valid)
		assert.Equal(t, hashing.HashToken(*checkout.AccessToken), donation.AccessTokenHash.Bytes)
		assert.NotContains(t, string(donation.AccessTokenHash.Bytes), *checkout.AccessToken,
			"the plaintext token must not be recoverable from the stored value")
	})
}

// References must be unguessable: they travel in callback URLs and identify a
// transaction to the provider.
func TestDonationReferencesAreUnique(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		seen := map[string]struct{}{}

		for i := 0; i < 20; i++ {
			checkout := startDonation(t, s)
			_, duplicate := seen[*checkout.Reference]
			require.False(t, duplicate, "reference %s repeated", *checkout.Reference)
			seen[*checkout.Reference] = struct{}{}
		}
	})
}

func TestDonationAmountIsBounded(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		for _, amount := range []int64{1, s.Config.Payment.MinDonationKobo - 1, s.Config.Payment.MaxDonationKobo + 1} {
			res := test.PerformRequest(t, s, "POST", "/api/v1/donations/checkout", test.GenericPayload{
				"email": "donor@example.com", "amountKobo": amount,
			}, nil)

			assert.Equal(t, http.StatusBadRequest, res.Result().StatusCode,
				"amount %d must be rejected", amount)
		}
	})
}

// The status endpoint is token-scoped. The reference alone must not be enough:
// it travels in the callback URL, so anyone who observes one would otherwise be
// able to read a stranger's donation.
func TestDonationStatusRequiresTheAccessToken(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		checkout := startDonation(t, s)

		assert.Equal(t, http.StatusOK, readStatus(t, s, *checkout.Reference, *checkout.AccessToken).StatusCode)

		// A wrong token and an unknown reference must be indistinguishable, or
		// this becomes a reference oracle.
		wrongToken := readStatus(t, s, *checkout.Reference, "11111111-1111-4111-8111-111111111111")
		unknownRef := readStatus(t, s, "cld_don_NOSUCHREFERENCE", *checkout.AccessToken)

		assert.Equal(t, http.StatusNotFound, wrongToken.StatusCode)
		assert.Equal(t, http.StatusNotFound, unknownRef.StatusCode)
	})
}

// The full happy path: checkout, the provider settles, the webhook arrives.
func TestDonationSettlesViaWebhook(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		checkout := startDonation(t, s)

		body := []byte(fmt.Sprintf(
			`{"event":"charge.success","data":{"id":"evt-1","reference":"%s","status":"success","amount":%d,"currency":"NGN"}}`,
			*checkout.Reference, oneHundredNaira))

		headers := http.Header{}
		headers.Set(paystack.SignatureHeader, mockPayments(t, s).SignForTest(body))

		res := test.PerformRequestWithRawBody(t, s, "POST", "/webhooks/payments",
			bytesReader(body), headers, nil)
		require.Equal(t, http.StatusOK, res.Result().StatusCode)

		status := readStatus(t, s, *checkout.Reference, *checkout.AccessToken)
		require.Equal(t, http.StatusOK, status.StatusCode)

		donation, err := models.Donations(
			models.DonationWhere.Reference.EQ(*checkout.Reference),
		).One(t.Context(), s.DB)
		require.NoError(t, err)
		assert.Equal(t, models.DonationStatusCompleted, donation.Status)
	})
}

// The case that makes a dropped webhook survivable: the payer returns, and the
// status read asks the provider server-to-server what actually happened. NO
// webhook is delivered in this test at all.
func TestDonationSettlesViaVerifyOnReturnWhenNoWebhookArrives(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		checkout := startDonation(t, s)

		// The provider knows it succeeded; we have not been told.
		mockPayments(t, s).SettleForTest(*checkout.Reference, payment.StatusSucceeded, oneHundredNaira)

		res := readStatus(t, s, *checkout.Reference, *checkout.AccessToken)
		require.Equal(t, http.StatusOK, res.StatusCode)

		donation, err := models.Donations(
			models.DonationWhere.Reference.EQ(*checkout.Reference),
		).One(t.Context(), s.DB)
		require.NoError(t, err)
		assert.Equal(t, models.DonationStatusCompleted, donation.Status,
			"reading status must reconcile with the provider when no webhook arrived")

		paymentRow, err := models.Payments(
			models.PaymentWhere.Reference.EQ(*checkout.Reference),
		).One(t.Context(), s.DB)
		require.NoError(t, err)
		assert.Equal(t, models.PaymentStatusSucceeded, paymentRow.Status)
	})
}

// Both paths converge on one guarded routine, so a webhook arriving AFTER a
// verify-on-return must not settle anything twice.
func TestVerifyThenWebhookSettlesExactlyOnce(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		checkout := startDonation(t, s)
		mockPayments(t, s).SettleForTest(*checkout.Reference, payment.StatusSucceeded, oneHundredNaira)

		// Verify-on-return settles it.
		require.Equal(t, http.StatusOK, readStatus(t, s, *checkout.Reference, *checkout.AccessToken).StatusCode)

		paymentRow, err := models.Payments(
			models.PaymentWhere.Reference.EQ(*checkout.Reference),
		).One(t.Context(), s.DB)
		require.NoError(t, err)
		settledAt := paymentRow.SucceededAt.Time

		// Now the webhook finally turns up.
		body := []byte(fmt.Sprintf(
			`{"event":"charge.success","data":{"id":"evt-late","reference":"%s","status":"success","amount":%d,"currency":"NGN"}}`,
			*checkout.Reference, oneHundredNaira))

		headers := http.Header{}
		headers.Set(paystack.SignatureHeader, mockPayments(t, s).SignForTest(body))

		res := test.PerformRequestWithRawBody(t, s, "POST", "/webhooks/payments", bytesReader(body), headers, nil)
		require.Equal(t, http.StatusOK, res.Result().StatusCode)

		require.NoError(t, paymentRow.Reload(t.Context(), s.DB))
		assert.Equal(t, models.PaymentStatusSucceeded, paymentRow.Status)
		assert.Equal(t, settledAt.UnixNano(), paymentRow.SucceededAt.Time.UnixNano(),
			"the second path must not re-settle and move the timestamp")
	})
}

// A donation the provider says failed must not complete.
func TestDonationDoesNotCompleteWhenTheProviderSaysFailed(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		checkout := startDonation(t, s)
		mockPayments(t, s).SettleForTest(*checkout.Reference, payment.StatusFailed, oneHundredNaira)

		require.Equal(t, http.StatusOK, readStatus(t, s, *checkout.Reference, *checkout.AccessToken).StatusCode)

		donation, err := models.Donations(
			models.DonationWhere.Reference.EQ(*checkout.Reference),
		).One(t.Context(), s.DB)
		require.NoError(t, err)
		assert.Equal(t, models.DonationStatusPending, donation.Status)
	})
}

// A provider outage at checkout must leave no half-created state that looks paid.
func TestDonationProviderFailureLeavesNothingPayable(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		mockPayments(t, s).FailInitialize = true

		res := test.PerformRequest(t, s, "POST", "/api/v1/donations/checkout", test.GenericPayload{
			"email": "donor@example.com", "amountKobo": oneHundredNaira,
		}, nil)
		assert.Equal(t, http.StatusBadGateway, res.Result().StatusCode)

		// A pending donation may remain -- no money moved and the intent expires --
		// but nothing may be completed.
		completed, err := models.Donations(
			models.DonationWhere.Status.EQ(models.DonationStatusCompleted),
		).Count(t.Context(), s.DB)
		require.NoError(t, err)
		assert.Equal(t, int64(0), completed)
	})
}

func TestGetDonationTiersIsPublic(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		res := test.PerformRequest(t, s, "GET", "/api/v1/donations/tiers", nil, nil)
		require.Equal(t, http.StatusOK, res.Result().StatusCode)

		var tiers types.DonationTierList
		test.ParseResponseAndValidate(t, res, &tiers)
		assert.NotNil(t, tiers.Items)
	})
}

func TestAdminDonationsRequiresAdminScope(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		res := test.PerformRequest(t, s, "GET", "/api/v1/admin/donations", nil, nil)
		assert.Equal(t, http.StatusUnauthorized, res.Result().StatusCode)
	})
}

// Donations must not leak into an unrelated listing.
func TestDonationsAreNotVisibleWithoutAdminScope(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		startDonation(t, s)

		count, err := models.Donations(qm.Where("email = ?", "donor@example.com")).Count(t.Context(), s.DB)
		require.NoError(t, err)
		require.Positive(t, count)

		res := test.PerformRequest(t, s, "GET", "/api/v1/admin/donations", nil, nil)
		assert.Equal(t, http.StatusUnauthorized, res.Result().StatusCode)
	})
}

// bytesReader keeps the webhook calls readable.
func bytesReader(body []byte) io.Reader { return bytes.NewReader(body) }
