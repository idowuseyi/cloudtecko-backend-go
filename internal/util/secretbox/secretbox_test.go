package secretbox_test

import (
	"encoding/base64"
	"testing"

	"github.com/idowuseyi/cloudtecko-backend-go/internal/util/secretbox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testSecret = "a-configured-encryption-secret-of-any-length"

func TestSealAndOpenRoundTrip(t *testing.T) {
	t.Parallel()

	box, err := secretbox.New(testSecret)
	require.NoError(t, err)

	for _, plaintext := range []string{
		"paystack-email-token-value",
		"",
		"a value with spaces, punctuation & unicode: náira ₦",
	} {
		sealed, err := box.Seal(plaintext)
		require.NoError(t, err)

		opened, err := box.Open(sealed)
		require.NoError(t, err)
		assert.Equal(t, plaintext, opened)
	}
}

// The plaintext must not be recoverable by reading the stored value.
func TestSealedValueDoesNotContainThePlaintext(t *testing.T) {
	t.Parallel()

	box, err := secretbox.New(testSecret)
	require.NoError(t, err)

	sealed, err := box.Seal("paystack-email-token-value")
	require.NoError(t, err)

	assert.NotContains(t, sealed, "paystack")

	raw, err := base64.StdEncoding.DecodeString(sealed)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "paystack")
}

// Nonce reuse under one key is catastrophic for GCM, so two seals of the same
// plaintext must differ.
func TestSealUsesAFreshNonceEveryTime(t *testing.T) {
	t.Parallel()

	box, err := secretbox.New(testSecret)
	require.NoError(t, err)

	seen := map[string]struct{}{}

	for range 200 {
		sealed, err := box.Seal("identical plaintext")
		require.NoError(t, err)

		_, duplicate := seen[sealed]
		require.False(t, duplicate, "a nonce was reused")
		seen[sealed] = struct{}{}
	}
}

// GCM is authenticated: a modified ciphertext must fail to open rather than
// decrypt to garbage.
func TestTamperedCiphertextIsRejected(t *testing.T) {
	t.Parallel()

	box, err := secretbox.New(testSecret)
	require.NoError(t, err)

	sealed, err := box.Seal("paystack-email-token-value")
	require.NoError(t, err)

	raw, err := base64.StdEncoding.DecodeString(sealed)
	require.NoError(t, err)

	// Flip a bit in the ciphertext body, past the nonce.
	raw[len(raw)-1] ^= 0x01
	tampered := base64.StdEncoding.EncodeToString(raw)

	_, err = box.Open(tampered)
	assert.ErrorIs(t, err, secretbox.ErrDecryptFailed)
}

func TestWrongKeyCannotOpen(t *testing.T) {
	t.Parallel()

	box, err := secretbox.New(testSecret)
	require.NoError(t, err)

	sealed, err := box.Seal("paystack-email-token-value")
	require.NoError(t, err)

	other, err := secretbox.New("a completely different secret")
	require.NoError(t, err)

	_, err = other.Open(sealed)
	assert.ErrorIs(t, err, secretbox.ErrDecryptFailed)
}

// Every failure mode reports the same error: distinguishing them would be a
// decryption oracle.
func TestMalformedInputIsIndistinguishableFromTampering(t *testing.T) {
	t.Parallel()

	box, err := secretbox.New(testSecret)
	require.NoError(t, err)

	for _, input := range []string{"", "not-base64!!", base64.StdEncoding.EncodeToString([]byte("short"))} {
		_, err := box.Open(input)
		assert.ErrorIs(t, err, secretbox.ErrDecryptFailed, "input %q", input)
	}
}

// A missing key must be fatal, never a silent fall back to plaintext.
func TestMissingKeyIsAnError(t *testing.T) {
	t.Parallel()

	_, err := secretbox.New("")
	assert.ErrorIs(t, err, secretbox.ErrKeyMissing)
}
