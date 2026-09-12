// Package payment abstracts the payment provider behind the money flows.
package payment

import (
	"context"
	"errors"
	"time"
)

var (
	// ErrSignatureInvalid means a webhook body did not verify against the
	// provider's signature. It is returned by ParseWebhook so a caller cannot
	// accidentally act on unverified input.
	ErrSignatureInvalid = errors.New("payment: webhook signature is invalid")

	// ErrTransactionNotFound means the provider has no record of the reference.
	ErrTransactionNotFound = errors.New("payment: transaction not found")

	// ErrUnsupportedEvent means the webhook is well-formed and authentic but
	// carries an event type this application does not act on. Callers record it
	// and return 200: the alternative -- a non-2xx -- would make the provider
	// retry an event we will never handle, for 72 hours.
	ErrUnsupportedEvent = errors.New("payment: unsupported webhook event")
)

// Status is the provider-independent outcome of a transaction.
type Status string

const (
	StatusPending   Status = "pending"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusAbandoned Status = "abandoned"
	StatusReversed  Status = "reversed"
)

// EventType is the provider-independent webhook event.
type EventType string

const (
	EventChargeSucceeded      EventType = "charge.succeeded"
	EventChargeFailed         EventType = "charge.failed"
	EventRefundProcessed      EventType = "refund.processed"
	EventSubscriptionCreated  EventType = "subscription.created"
	EventSubscriptionDisabled EventType = "subscription.disabled"
	EventInvoiceFailed        EventType = "invoice.payment_failed"
)

// InitializeRequest starts a payment.
//
// Amount is in the currency's minor unit (kobo for NGN). Reference is OURS, not
// the provider's: we generate it, we look up by it, and we never trust a
// reference supplied by a client.
type InitializeRequest struct {
	Reference   string
	AmountMinor int64
	Currency    string
	Email       string
	CallbackURL string
	Metadata    map[string]string
}

// InitializeResult is where to send the payer.
type InitializeResult struct {
	AuthorizationURL  string
	ProviderReference string
}

// Transaction is the provider's own view of a payment, used both by verify and
// by webhook parsing so the two paths converge on one shape -- and therefore on
// one fulfilment code path.
type Transaction struct {
	Reference         string
	ProviderReference string
	ProviderID        string
	Status            Status
	AmountMinor       int64
	Currency          string
	Channel           string
	AuthorizationCode string
	PaidAt            time.Time
	FailureReason     string
}

// WebhookEvent is an authenticated, parsed provider notification.
type WebhookEvent struct {
	Type EventType
	// IdempotencyKey identifies this event for de-duplication. It must be stable
	// across the provider's retries and distinct between genuinely different
	// events -- which rules out hashing the body, since any re-serialisation or
	// recomputed field yields a fresh value and silently defeats the check.
	IdempotencyKey   string
	Transaction      Transaction
	SubscriptionCode string
	RefundedMinor    int64
	Raw              []byte
}

// PlanRequest creates a recurring plan.
type PlanRequest struct {
	Name        string
	AmountMinor int64
	Currency    string
	Interval    string
}

// PlanResult identifies the created plan.
type PlanResult struct {
	PlanCode string
}

// RefundRequest asks the provider to return money.
type RefundRequest struct {
	Reference string
	// AmountMinor of 0 means a full refund.
	AmountMinor int64
	Reason      string
}

// RefundResult identifies the refund.
type RefundResult struct {
	ProviderRefundID string
	Status           string
}

// Provider is the seam every money flow talks through.
//
// VerifyTransaction and ParseWebhook deliberately return the SAME Transaction
// shape: the redirect path and the webhook path must converge on one idempotent
// fulfilment routine, because a webhook can be delayed, dropped, or delivered
// twice, and the redirect proves nothing on its own.
type Provider interface {
	Name() string
	InitializeTransaction(ctx context.Context, request InitializeRequest) (InitializeResult, error)
	VerifyTransaction(ctx context.Context, reference string) (Transaction, error)

	// ParseWebhook verifies the signature over the RAW body and parses it.
	// Signature verification is inside this call, not beside it, so no caller can
	// forget to do it.
	ParseWebhook(rawBody []byte, signature string) (WebhookEvent, error)

	CreatePlan(ctx context.Context, request PlanRequest) (PlanResult, error)
	DisableSubscription(ctx context.Context, subscriptionCode string, emailToken string) error
	RefundTransaction(ctx context.Context, request RefundRequest) (RefundResult, error)
}
