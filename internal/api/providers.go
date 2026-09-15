package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/dropbox/godropbox/time2"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/auth"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/config"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/i18n"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/mailer"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/payment"
	paymentmock "github.com/idowuseyi/cloudtecko-backend-go/internal/payment/mock"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/payment/paystack"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/persistence"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/push"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/push/provider"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/storage"
	storagemock "github.com/idowuseyi/cloudtecko-backend-go/internal/storage/mock"
	storages3 "github.com/idowuseyi/cloudtecko-backend-go/internal/storage/s3"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util/secretbox"
	"github.com/rs/zerolog/log"
)

// PROVIDERS - define here only providers that for various reasons (e.g. cyclic dependency) can't live in their corresponding packages
// or for wrapping providers that only accept sub-configs to prevent the requirements for defining providers for sub-configs.
// https://github.com/google/wire/blob/main/docs/guide.md#defining-providers

// NewStorage creates the object store behind the media pipeline.
//
// The mock is the default, mirroring how the push service behaves: a developer
// with no object-store credentials still gets a working server rather than a
// boot failure. Production sets SERVER_STORAGE_USE_MOCK=false and supplies real
// credentials, and misconfiguring those fails loudly here at startup rather than
// silently at the first upload.
//
// implementations (mock or S3) chosen at runtime from config.
//
//nolint:ireturn // returning the interface is the point: wire injects one of two
func NewStorage(cfg config.Server) (storage.Storage, error) {
	if cfg.Storage.UseMockProvider {
		log.Warn().Msg("Initializing mock storage provider (uploads are kept in memory and lost on restart)")

		return storagemock.New(cfg.Storage.PublicBaseURL), nil
	}

	store, err := storages3.New(storages3.Config{
		Endpoint:      cfg.Storage.Endpoint,
		AccessKey:     cfg.Storage.AccessKey,
		SecretKey:     cfg.Storage.SecretKey,
		Bucket:        cfg.Storage.Bucket,
		Region:        cfg.Storage.Region,
		UseSSL:        cfg.Storage.UseSSL,
		PublicBaseURL: cfg.Storage.PublicBaseURL,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create storage provider: %w", err)
	}

	return store, nil
}

// NewSecretBox builds the encryptor for values that must be recoverable.
//
// Fails at startup when no key is configured and the mock provider is off,
// rather than degrading to plaintext: silently storing a live payment credential
// unencrypted is worse than refusing to boot.
func NewSecretBox(cfg config.Server) (*secretbox.Box, error) {
	secret := cfg.Payment.SecretEncryptionKey
	if secret == "" {
		if !cfg.Payment.UseMockProvider {
			return nil, errors.New("SERVER_PAYMENT_SECRET_ENCRYPTION_KEY is required when the mock payment provider is disabled")
		}

		log.Warn().Msg("Using a development encryption key: set SERVER_PAYMENT_SECRET_ENCRYPTION_KEY before handling real subscriptions")

		secret = "development-only-encryption-key"
	}

	box, err := secretbox.New(secret)
	if err != nil {
		return nil, fmt.Errorf("failed to create secret box: %w", err)
	}

	return box, nil
}

// NewPaymentProvider creates the payment provider.
//
// The mock is the default, matching how push and storage behave: a developer
// without provider credentials still gets a working server. Production sets
// SERVER_PAYMENT_USE_MOCK=false and supplies a secret key, and a missing key
// then fails loudly at startup rather than silently at the first charge.
//
// implementations chosen at runtime from config.
//
//nolint:ireturn // returning the interface is the point: wire injects one of two
func NewPaymentProvider(cfg config.Server) (payment.Provider, error) {
	if cfg.Payment.UseMockProvider {
		log.Warn().Msg("Initializing mock payment provider (no real money moves)")

		return paymentmock.New(cfg.Payment.MockSecretKey), nil
	}

	if cfg.Payment.SecretKey == "" {
		return nil, errors.New("SERVER_PAYMENT_SECRET_KEY is required when the mock payment provider is disabled")
	}

	return paystack.New(paystack.Config{
		SecretKey: cfg.Payment.SecretKey,
		BaseURL:   cfg.Payment.BaseURL,
		Timeout:   cfg.Payment.RequestTimeout,
	}), nil
}

// NewPush creates an instance of the push service and registers the configured push providers.
func NewPush(cfg config.Server, db *sql.DB) (*push.Service, error) {
	pusher := push.New(db)

	if cfg.Push.UseFCMProvider {
		fcmProvider, err := provider.NewFCM(cfg.FCMConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to create FCM provider: %w", err)
		}
		pusher.RegisterProvider(fcmProvider)
	}

	if cfg.Push.UseMockProvider {
		log.Warn().Msg("Initializing mock push provider")
		mockProvider := provider.NewMock(push.ProviderTypeFCM)
		pusher.RegisterProvider(mockProvider)
	}

	if pusher.GetProviderCount() < 1 {
		log.Warn().Msg("No providers registered for push service")
	}

	return pusher, nil
}

func NewClock(t ...*testing.T) time2.Clock {
	var clock time2.Clock

	useMock := len(t) > 0 && t[0] != nil

	if useMock {
		clock = time2.NewMockClock(time.Now())
	} else {
		clock = time2.DefaultClock
	}

	return clock
}

func NewAuthService(config config.Server, db *sql.DB, clock time2.Clock) *auth.Service {
	return auth.NewService(config, db, clock)
}

func NewMailer(config config.Server) (*mailer.Mailer, error) {
	return mailer.NewWithConfig(config.Mailer, config.SMTP)
}

func NewDB(config config.Server) (*sql.DB, error) {
	return persistence.NewDB(context.Background(), config.Database)
}

func NewI18N(config config.Server) (*i18n.Service, error) {
	return i18n.New(config.I18n)
}

func NoTest() []*testing.T {
	return nil
}
