package api_test

import (
	"testing"

	"github.com/idowuseyi/cloudtecko-backend-go/internal/api"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The mock must never be what a deployment gets by accident.
//
// Its webhook signature check uses a locally-configured key, so reaching it in
// production means anyone who can POST to /webhooks/payments can sign their own
// charge.success and have an order fulfilled without paying. Push and storage
// default to their mocks because those degrade to a no-op; this one does not.
func TestPaymentProviderIsNotMockedByDefault(t *testing.T) {
	t.Parallel()

	cfg := config.DefaultServiceConfigFromEnv()

	assert.False(t, cfg.Payment.UseMockProvider,
		"SERVER_PAYMENT_USE_MOCK must default to false")
	assert.Empty(t, cfg.Payment.MockSecretKey,
		"a default mock signing key is a usable forgery key for anyone who reads the repository")
}

// Enabling the mock without a key must refuse to boot rather than sign with "".
func TestMockPaymentProviderRequiresAnExplicitKey(t *testing.T) {
	t.Parallel()

	cfg := config.DefaultServiceConfigFromEnv()
	cfg.Payment.UseMockProvider = true
	cfg.Payment.MockSecretKey = ""

	provider, err := api.NewPaymentProvider(cfg)

	require.Error(t, err, "the mock must not be constructible without a signing key")
	assert.Nil(t, provider)
	assert.Contains(t, err.Error(), "SERVER_PAYMENT_MOCK_SECRET_KEY")
}

// The real provider is equally strict about its own key.
func TestRealPaymentProviderRequiresASecretKey(t *testing.T) {
	t.Parallel()

	cfg := config.DefaultServiceConfigFromEnv()
	cfg.Payment.UseMockProvider = false
	cfg.Payment.SecretKey = ""

	provider, err := api.NewPaymentProvider(cfg)

	require.Error(t, err)
	assert.Nil(t, provider)
	assert.Contains(t, err.Error(), "SERVER_PAYMENT_SECRET_KEY")
}

// With a key supplied, the mock builds -- the refusal is about the key, not
// about the mock being unavailable to developers who want it.
func TestMockPaymentProviderBuildsWithAKey(t *testing.T) {
	t.Parallel()

	cfg := config.DefaultServiceConfigFromEnv()
	cfg.Payment.UseMockProvider = true
	cfg.Payment.MockSecretKey = "a-local-development-key"

	provider, err := api.NewPaymentProvider(cfg)

	require.NoError(t, err)
	assert.NotNil(t, provider)
}
