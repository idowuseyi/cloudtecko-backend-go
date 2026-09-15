// Package mock implements payment.Provider in memory, so the whole money
// pipeline is testable and locally runnable without provider credentials.
package mock

import (
	"context"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/idowuseyi/cloudtecko-backend-go/internal/payment"
)

const providerName = "mock"

// Provider records what it was asked to do and answers plausibly.
//
// It mirrors the real provider's awkward properties rather than smoothing them
// over, because those are exactly what the calling code has to survive: the
// signature covers the body with no timestamp (so replay is possible and must be
// defended against elsewhere), and the same event may arrive more than once.
type Provider struct {
	mutex sync.RWMutex

	// SecretKey signs webhook bodies, the same way Paystack uses its secret key.
	SecretKey string

	transactions map[string]payment.Transaction

	// FailInitialize makes InitializeTransaction return an error, so callers can
	// be tested for leaving no half-created payment behind.
	FailInitialize bool
}

func New(secretKey string) *Provider {
	return &Provider{
		SecretKey:    secretKey,
		transactions: map[string]payment.Transaction{},
	}
}

func (p *Provider) Name() string { return providerName }

func (p *Provider) InitializeTransaction(_ context.Context, request payment.InitializeRequest) (payment.InitializeResult, error) {
	if p.FailInitialize {
		return payment.InitializeResult{}, fmt.Errorf("mock: initialize failed on purpose")
	}

	p.mutex.Lock()
	defer p.mutex.Unlock()

	p.transactions[request.Reference] = payment.Transaction{
		Reference:         request.Reference,
		ProviderReference: request.Reference,
		ProviderID:        "mock-" + request.Reference,
		Status:            payment.StatusPending,
		AmountMinor:       request.AmountMinor,
		Currency:          request.Currency,
	}

	return payment.InitializeResult{
		AuthorizationURL:  "https://checkout.mock.test/pay/" + request.Reference,
		ProviderReference: request.Reference,
	}, nil
}

// SettleForTest marks a transaction as the provider would after a real payment.
func (p *Provider) SettleForTest(reference string, status payment.Status, amountMinor int64) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	transaction := p.transactions[reference]
	transaction.Reference = reference
	transaction.ProviderReference = reference
	transaction.ProviderID = "mock-" + reference
	transaction.Status = status
	transaction.AmountMinor = amountMinor
	transaction.Currency = "NGN"
	transaction.Channel = "card"
	transaction.PaidAt = time.Now()

	p.transactions[reference] = transaction
}

func (p *Provider) VerifyTransaction(_ context.Context, reference string) (payment.Transaction, error) {
	p.mutex.RLock()
	defer p.mutex.RUnlock()

	transaction, exists := p.transactions[reference]
	if !exists {
		return payment.Transaction{}, payment.ErrTransactionNotFound
	}

	return transaction, nil
}

// SignForTest produces the signature a caller must present alongside a body,
// so tests exercise the real verification path rather than bypassing it.
func (p *Provider) SignForTest(rawBody []byte) string {
	mac := hmac.New(sha512.New, []byte(p.SecretKey))
	mac.Write(rawBody)

	return hex.EncodeToString(mac.Sum(nil))
}

type mockEnvelope struct {
	Event string `json:"event"`
	Data  struct {
		ID               string `json:"id"`
		Reference        string `json:"reference"`
		Status           string `json:"status"`
		Amount           int64  `json:"amount"`
		Currency         string `json:"currency"`
		SubscriptionCode string `json:"subscription_code"`
		EmailToken       string `json:"email_token"`
		CustomerCode     string `json:"customer_code"`
		AmountRefunded   int64  `json:"amount_refunded"`
	} `json:"data"`
}

func (p *Provider) ParseWebhook(rawBody []byte, signature string) (payment.WebhookEvent, error) {
	mac := hmac.New(sha512.New, []byte(p.SecretKey))
	mac.Write(rawBody)

	if !hmac.Equal([]byte(hex.EncodeToString(mac.Sum(nil))), []byte(signature)) {
		return payment.WebhookEvent{}, payment.ErrSignatureInvalid
	}

	var envelope mockEnvelope
	if err := json.Unmarshal(rawBody, &envelope); err != nil {
		return payment.WebhookEvent{}, fmt.Errorf("mock: failed to parse webhook: %w", err)
	}

	eventType, known := map[string]payment.EventType{
		"charge.success":         payment.EventChargeSucceeded,
		"charge.failed":          payment.EventChargeFailed,
		"refund.processed":       payment.EventRefundProcessed,
		"subscription.create":    payment.EventSubscriptionCreated,
		"subscription.disable":   payment.EventSubscriptionDisabled,
		"invoice.payment_failed": payment.EventInvoiceFailed,
	}[envelope.Event]
	if !known {
		return payment.WebhookEvent{}, payment.ErrUnsupportedEvent
	}

	identity := envelope.Data.ID
	if identity == "" {
		identity = envelope.Data.Reference
	}

	status := payment.StatusPending
	switch envelope.Data.Status {
	case "success":
		status = payment.StatusSucceeded
	case "failed":
		status = payment.StatusFailed
	case "abandoned":
		status = payment.StatusAbandoned
	}

	return payment.WebhookEvent{
		Type:           eventType,
		IdempotencyKey: envelope.Event + ":" + identity,
		Transaction: payment.Transaction{
			Reference:         envelope.Data.Reference,
			ProviderReference: envelope.Data.Reference,
			ProviderID:        identity,
			Status:            status,
			AmountMinor:       envelope.Data.Amount,
			Currency:          envelope.Data.Currency,
			Channel:           "card",
			PaidAt:            time.Now(),
		},
		SubscriptionCode:       envelope.Data.SubscriptionCode,
		SubscriptionEmailToken: envelope.Data.EmailToken,
		CustomerCode:           envelope.Data.CustomerCode,
		RefundedMinor:          envelope.Data.AmountRefunded,
		Raw:                    rawBody,
	}, nil
}

func (p *Provider) CreatePlan(_ context.Context, request payment.PlanRequest) (payment.PlanResult, error) {
	return payment.PlanResult{PlanCode: "PLN_mock_" + request.Interval}, nil
}

func (p *Provider) DisableSubscription(_ context.Context, subscriptionCode string, emailToken string) error {
	if subscriptionCode == "" || emailToken == "" {
		// Mirrors the real constraint: Paystack needs BOTH, and losing the token
		// means the subscription cannot be cancelled through our UI at all.
		return fmt.Errorf("mock: both subscription code and email token are required")
	}

	return nil
}

func (p *Provider) RefundTransaction(_ context.Context, request payment.RefundRequest) (payment.RefundResult, error) {
	return payment.RefundResult{
		ProviderRefundID: "mock-refund-" + request.Reference,
		Status:           "processed",
	}, nil
}
