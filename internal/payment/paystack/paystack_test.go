package paystack_test

import (
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"testing"

	"github.com/idowuseyi/cloudtecko-backend-go/internal/payment"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/payment/paystack"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testSecretKey = "sk_test_0123456789abcdef0123456789abcdef01234567"

// sign reproduces what Paystack does: HMAC-SHA512 of the raw body, keyed by the
// secret key, hex-encoded.
func sign(t *testing.T, body []byte) string {
	t.Helper()

	mac := hmac.New(sha512.New, []byte(testSecretKey))
	_, err := mac.Write(body)
	require.NoError(t, err)

	return hex.EncodeToString(mac.Sum(nil))
}

func TestVerifyWebhookSignature(t *testing.T) {
	t.Parallel()

	provider := paystack.New(paystack.Config{SecretKey: testSecretKey})
	body := []byte(`{"event":"charge.success","data":{"reference":"abc","amount":10000}}`)

	assert.True(t, provider.VerifyWebhookSignature(body, sign(t, body)))

	// Wrong key.
	other := paystack.New(paystack.Config{SecretKey: "sk_test_someone_elses_key"})
	assert.False(t, other.VerifyWebhookSignature(body, sign(t, body)))

	// Tampered body: a single changed digit must invalidate the signature. This
	// is the amount-tampering case -- an attacker who replays a real ₦100 body
	// with the amount edited to ₦1,000,000 must not verify.
	tampered := []byte(`{"event":"charge.success","data":{"reference":"abc","amount":100000000}}`)
	assert.False(t, provider.VerifyWebhookSignature(tampered, sign(t, body)))

	// Empty and malformed signatures.
	assert.False(t, provider.VerifyWebhookSignature(body, ""))
	assert.False(t, provider.VerifyWebhookSignature(body, "not-hex"))
}

// The signature is over the bytes AS RECEIVED. Re-serialising the parsed JSON
// changes key order and whitespace, so a handler that verifies against a
// re-marshalled body can never match -- the single most common way this
// integration fails.
func TestSignatureIsOverRawBytesNotReserialisedJSON(t *testing.T) {
	t.Parallel()

	provider := paystack.New(paystack.Config{SecretKey: testSecretKey})

	raw := []byte("{\n  \"event\": \"charge.success\",\n  \"data\": {\"reference\": \"abc\", \"amount\": 10000}\n}")
	signature := sign(t, raw)

	require.True(t, provider.VerifyWebhookSignature(raw, signature), "the raw bytes verify")

	compact := []byte(`{"event":"charge.success","data":{"reference":"abc","amount":10000}}`)
	assert.False(t, provider.VerifyWebhookSignature(compact, signature),
		"semantically identical JSON with different formatting must NOT verify")
}

func TestParseWebhookRejectsBadSignatureBeforeParsing(t *testing.T) {
	t.Parallel()

	provider := paystack.New(paystack.Config{SecretKey: testSecretKey})

	// Deliberately unparseable: if the signature check did not come first, this
	// would surface as a JSON error instead, proving the order was wrong.
	_, err := provider.ParseWebhook([]byte("this is not json at all"), "deadbeef")
	require.ErrorIs(t, err, payment.ErrSignatureInvalid)
}

func TestParseWebhookChargeSuccess(t *testing.T) {
	t.Parallel()

	provider := paystack.New(paystack.Config{SecretKey: testSecretKey})

	body := []byte(`{
	  "event": "charge.success",
	  "data": {
	    "id": 302961,
	    "reference": "cld_don_01H",
	    "status": "success",
	    "amount": 10000,
	    "currency": "NGN",
	    "channel": "card",
	    "paid_at": "2026-09-12T10:00:00.000Z",
	    "authorization": {"authorization_code": "AUTH_xyz"}
	  }
	}`)

	event, err := provider.ParseWebhook(body, sign(t, body))
	require.NoError(t, err)

	assert.Equal(t, payment.EventChargeSucceeded, event.Type)
	assert.Equal(t, payment.StatusSucceeded, event.Transaction.Status)
	assert.Equal(t, int64(10000), event.Transaction.AmountMinor, "amount stays in kobo")
	assert.Equal(t, "NGN", event.Transaction.Currency)
	assert.Equal(t, "AUTH_xyz", event.Transaction.AuthorizationCode)

	// The key is the provider's event identity, NOT a digest of the body.
	assert.Equal(t, "charge.success:302961", event.IdempotencyKey)
}

// Paystack retries the same delivery. The key must be identical across retries,
// or de-duplication silently does nothing.
func TestIdempotencyKeyIsStableAcrossRetries(t *testing.T) {
	t.Parallel()

	provider := paystack.New(paystack.Config{SecretKey: testSecretKey})
	body := []byte(`{"event":"charge.success","data":{"id":302961,"reference":"r1","status":"success","amount":10000,"currency":"NGN"}}`)

	first, err := provider.ParseWebhook(body, sign(t, body))
	require.NoError(t, err)

	second, err := provider.ParseWebhook(body, sign(t, body))
	require.NoError(t, err)

	assert.Equal(t, first.IdempotencyKey, second.IdempotencyKey)
}

// ...and distinct between genuinely different events on the same transaction,
// or a later refund would be swallowed as a duplicate of the charge.
func TestIdempotencyKeyDiffersBetweenEventTypes(t *testing.T) {
	t.Parallel()

	provider := paystack.New(paystack.Config{SecretKey: testSecretKey})

	charge := []byte(`{"event":"charge.success","data":{"id":1,"reference":"r1","status":"success","amount":10000,"currency":"NGN"}}`)
	refund := []byte(`{"event":"refund.processed","data":{"id":1,"reference":"r1","status":"success","amount":10000,"currency":"NGN"}}`)

	chargeEvent, err := provider.ParseWebhook(charge, sign(t, charge))
	require.NoError(t, err)

	refundEvent, err := provider.ParseWebhook(refund, sign(t, refund))
	require.NoError(t, err)

	assert.NotEqual(t, chargeEvent.IdempotencyKey, refundEvent.IdempotencyKey)
}

// An authentic event we do not act on must be distinguishable from a forged one:
// the caller records it and returns 200, where a non-2xx would make Paystack
// retry for 72 hours something we will never handle.
func TestParseWebhookUnsupportedEventIsNotASignatureFailure(t *testing.T) {
	t.Parallel()

	provider := paystack.New(paystack.Config{SecretKey: testSecretKey})
	body := []byte(`{"event":"customer.identification.failed","data":{"id":9,"reference":"r9"}}`)

	_, err := provider.ParseWebhook(body, sign(t, body))
	require.ErrorIs(t, err, payment.ErrUnsupportedEvent)
	require.NotErrorIs(t, err, payment.ErrSignatureInvalid)
}

// An unrecognised status must never read as success: guessing optimistically
// about a payment state is how money gets fulfilled that was never captured.
func TestUnknownStatusIsPendingNotSucceeded(t *testing.T) {
	t.Parallel()

	provider := paystack.New(paystack.Config{SecretKey: testSecretKey})
	body := []byte(`{"event":"charge.success","data":{"id":5,"reference":"r5","status":"something_new","amount":10000,"currency":"NGN"}}`)

	event, err := provider.ParseWebhook(body, sign(t, body))
	require.NoError(t, err)

	assert.Equal(t, payment.StatusPending, event.Transaction.Status)
	assert.NotEqual(t, payment.StatusSucceeded, event.Transaction.Status)
}
