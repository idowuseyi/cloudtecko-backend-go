// Package paystack implements payment.Provider against Paystack.
package paystack

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/idowuseyi/cloudtecko-backend-go/internal/payment"
)

const (
	defaultBaseURL = "https://api.paystack.co"
	providerName   = "paystack"

	// SignatureHeader is where Paystack puts the HMAC of the raw body.
	SignatureHeader = "x-paystack-signature"

	// Paystack times a webhook out after 30 seconds and then retries -- every
	// 3 minutes for the first four attempts, then hourly for up to 72 hours.
	// Handlers must therefore be fast AND idempotent: a slow handler turns into
	// a duplicate handler.
	webhookTimeout = 30 * time.Second
)

// Provider talks to Paystack.
type Provider struct {
	client    *http.Client
	baseURL   string
	secretKey string
}

// Config carries the credentials.
type Config struct {
	SecretKey string
	BaseURL   string
	Timeout   time.Duration
}

func New(config Config) *Provider {
	baseURL := config.BaseURL
	if baseURL == "" {
		baseURL = defaultBaseURL
	}

	timeout := config.Timeout
	if timeout == 0 {
		timeout = webhookTimeout
	}

	return &Provider{
		client:    &http.Client{Timeout: timeout},
		baseURL:   strings.TrimSuffix(baseURL, "/"),
		secretKey: config.SecretKey,
	}
}

func (p *Provider) Name() string { return providerName }

// VerifyWebhookSignature checks an HMAC-SHA512 of the RAW request body, keyed by
// the Paystack SECRET key (Paystack has no separate webhook secret).
//
// Two things matter here and are easy to get wrong:
//
//   - It must run over the bytes as received. Re-marshalling the parsed JSON
//     will not reproduce Paystack's exact output -- key order, whitespace and
//     numeric formatting all differ -- so the HMAC would never match.
//   - hmac.Equal, never ==. A byte-by-byte comparison that returns early leaks
//     how much of the digest was correct.
//
// Note what this does NOT give us: the signature covers the body only, with no
// timestamp and no nonce. There is no replay window. Anyone who ever observes
// one valid delivery can replay it verbatim, forever, and it will verify. Replay
// protection is entirely ours, via the idempotency key and guarded state
// transitions.
func (p *Provider) VerifyWebhookSignature(rawBody []byte, signature string) bool {
	mac := hmac.New(sha512.New, []byte(p.secretKey))
	mac.Write(rawBody)

	expected := hex.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(expected), []byte(signature))
}

type webhookEnvelope struct {
	Event string `json:"event"`
	Data  struct {
		ID            json.Number `json:"id"`
		Reference     string      `json:"reference"`
		Status        string      `json:"status"`
		Amount        int64       `json:"amount"`
		Currency      string      `json:"currency"`
		Channel       string      `json:"channel"`
		PaidAt        string      `json:"paid_at"`
		GatewayResp   string      `json:"gateway_response"`
		Authorization struct {
			AuthorizationCode string `json:"authorization_code"`
		} `json:"authorization"`
		Subscription struct {
			SubscriptionCode string `json:"subscription_code"`
		} `json:"subscription"`
		SubscriptionCode string `json:"subscription_code"`
		Transaction      struct {
			Reference string `json:"reference"`
		} `json:"transaction"`
		AmountRefunded int64 `json:"amount_refunded"`
	} `json:"data"`
}

func (p *Provider) ParseWebhook(rawBody []byte, signature string) (payment.WebhookEvent, error) {
	if !p.VerifyWebhookSignature(rawBody, signature) {
		return payment.WebhookEvent{}, payment.ErrSignatureInvalid
	}

	var envelope webhookEnvelope
	if err := json.Unmarshal(rawBody, &envelope); err != nil {
		return payment.WebhookEvent{}, fmt.Errorf("paystack: failed to parse webhook: %w", err)
	}

	eventType, known := webhookEventTypes[envelope.Event]
	if !known {
		return payment.WebhookEvent{}, payment.ErrUnsupportedEvent
	}

	reference := envelope.Data.Reference
	if reference == "" {
		reference = envelope.Data.Transaction.Reference
	}

	subscriptionCode := envelope.Data.SubscriptionCode
	if subscriptionCode == "" {
		subscriptionCode = envelope.Data.Subscription.SubscriptionCode
	}

	// The de-duplication key is the provider's own event identity. Paystack sends
	// no dedicated event id, so it is composed from the event type and the data
	// id (falling back to the reference), which together are stable across
	// retries and distinct between genuinely different events.
	identity := envelope.Data.ID.String()
	if identity == "" {
		identity = reference
	}

	return payment.WebhookEvent{
		Type:           eventType,
		IdempotencyKey: envelope.Event + ":" + identity,
		Transaction: payment.Transaction{
			Reference:         reference,
			ProviderReference: reference,
			ProviderID:        envelope.Data.ID.String(),
			Status:            mapStatus(envelope.Data.Status),
			AmountMinor:       envelope.Data.Amount,
			Currency:          envelope.Data.Currency,
			Channel:           envelope.Data.Channel,
			AuthorizationCode: envelope.Data.Authorization.AuthorizationCode,
			PaidAt:            parsePaystackTime(envelope.Data.PaidAt),
			FailureReason:     envelope.Data.GatewayResp,
		},
		SubscriptionCode: subscriptionCode,
		RefundedMinor:    envelope.Data.AmountRefunded,
		Raw:              rawBody,
	}, nil
}

var webhookEventTypes = map[string]payment.EventType{
	"charge.success":         payment.EventChargeSucceeded,
	"charge.failed":          payment.EventChargeFailed,
	"refund.processed":       payment.EventRefundProcessed,
	"subscription.create":    payment.EventSubscriptionCreated,
	"subscription.disable":   payment.EventSubscriptionDisabled,
	"invoice.payment_failed": payment.EventInvoiceFailed,
}

// mapStatus folds Paystack's vocabulary into ours. Anything unrecognised is
// treated as pending rather than as success: guessing optimistically about a
// payment state is how money gets fulfilled that was never captured.
func mapStatus(status string) payment.Status {
	switch strings.ToLower(status) {
	case "success":
		return payment.StatusSucceeded
	case "failed":
		return payment.StatusFailed
	case "abandoned":
		return payment.StatusAbandoned
	case "reversed":
		return payment.StatusReversed
	default:
		return payment.StatusPending
	}
}

func parsePaystackTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}

	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05.000Z", "2006-01-02 15:04:05"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed
		}
	}

	return time.Time{}
}

func (p *Provider) InitializeTransaction(ctx context.Context, request payment.InitializeRequest) (payment.InitializeResult, error) {
	body := map[string]interface{}{
		"reference":    request.Reference,
		"amount":       request.AmountMinor,
		"currency":     request.Currency,
		"email":        request.Email,
		"callback_url": request.CallbackURL,
		"metadata":     request.Metadata,
	}

	var response struct {
		Status  bool   `json:"status"`
		Message string `json:"message"`
		Data    struct {
			AuthorizationURL string `json:"authorization_url"`
			Reference        string `json:"reference"`
		} `json:"data"`
	}

	if err := p.do(ctx, http.MethodPost, "/transaction/initialize", body, &response); err != nil {
		return payment.InitializeResult{}, err
	}

	if !response.Status {
		return payment.InitializeResult{}, fmt.Errorf("paystack: initialize rejected: %s", response.Message)
	}

	return payment.InitializeResult{
		AuthorizationURL:  response.Data.AuthorizationURL,
		ProviderReference: response.Data.Reference,
	}, nil
}

func (p *Provider) VerifyTransaction(ctx context.Context, reference string) (payment.Transaction, error) {
	var response struct {
		Status bool            `json:"status"`
		Data   webhookEnvelope `json:"-"`
		Raw    json.RawMessage `json:"data"`
	}

	if err := p.do(ctx, http.MethodGet, "/transaction/verify/"+reference, nil, &response); err != nil {
		return payment.Transaction{}, err
	}

	if !response.Status {
		return payment.Transaction{}, payment.ErrTransactionNotFound
	}

	var envelope webhookEnvelope
	if err := json.Unmarshal([]byte(`{"event":"charge.success","data":`+string(response.Raw)+`}`), &envelope); err != nil {
		return payment.Transaction{}, fmt.Errorf("paystack: failed to parse verify response: %w", err)
	}

	return payment.Transaction{
		Reference:         envelope.Data.Reference,
		ProviderReference: envelope.Data.Reference,
		ProviderID:        envelope.Data.ID.String(),
		Status:            mapStatus(envelope.Data.Status),
		AmountMinor:       envelope.Data.Amount,
		Currency:          envelope.Data.Currency,
		Channel:           envelope.Data.Channel,
		AuthorizationCode: envelope.Data.Authorization.AuthorizationCode,
		PaidAt:            parsePaystackTime(envelope.Data.PaidAt),
		FailureReason:     envelope.Data.GatewayResp,
	}, nil
}

func (p *Provider) CreatePlan(ctx context.Context, request payment.PlanRequest) (payment.PlanResult, error) {
	body := map[string]interface{}{
		"name":     request.Name,
		"amount":   request.AmountMinor,
		"currency": request.Currency,
		"interval": request.Interval,
	}

	var response struct {
		Status  bool   `json:"status"`
		Message string `json:"message"`
		Data    struct {
			PlanCode string `json:"plan_code"`
		} `json:"data"`
	}

	if err := p.do(ctx, http.MethodPost, "/plan", body, &response); err != nil {
		return payment.PlanResult{}, err
	}

	if !response.Status {
		return payment.PlanResult{}, fmt.Errorf("paystack: plan creation rejected: %s", response.Message)
	}

	return payment.PlanResult{PlanCode: response.Data.PlanCode}, nil
}

// DisableSubscription needs BOTH the subscription code and the email token.
// Paystack returns the token exactly once, at creation, which is why it has to
// be persisted rather than re-fetched.
func (p *Provider) DisableSubscription(ctx context.Context, subscriptionCode string, emailToken string) error {
	body := map[string]interface{}{"code": subscriptionCode, "token": emailToken}

	var response struct {
		Status  bool   `json:"status"`
		Message string `json:"message"`
	}

	if err := p.do(ctx, http.MethodPost, "/subscription/disable", body, &response); err != nil {
		return err
	}

	if !response.Status {
		return fmt.Errorf("paystack: disable subscription rejected: %s", response.Message)
	}

	return nil
}

func (p *Provider) RefundTransaction(ctx context.Context, request payment.RefundRequest) (payment.RefundResult, error) {
	body := map[string]interface{}{"transaction": request.Reference}
	if request.AmountMinor > 0 {
		body["amount"] = request.AmountMinor
	}

	if request.Reason != "" {
		body["merchant_note"] = request.Reason
	}

	var response struct {
		Status  bool   `json:"status"`
		Message string `json:"message"`
		Data    struct {
			ID     json.Number `json:"id"`
			Status string      `json:"status"`
		} `json:"data"`
	}

	if err := p.do(ctx, http.MethodPost, "/refund", body, &response); err != nil {
		return payment.RefundResult{}, err
	}

	if !response.Status {
		return payment.RefundResult{}, fmt.Errorf("paystack: refund rejected: %s", response.Message)
	}

	return payment.RefundResult{
		ProviderRefundID: response.Data.ID.String(),
		Status:           response.Data.Status,
	}, nil
}

func (p *Provider) do(ctx context.Context, method string, path string, body interface{}, out interface{}) error {
	var reader io.Reader

	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("paystack: failed to encode request: %w", err)
		}

		reader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, p.baseURL+path, reader)
	if err != nil {
		return fmt.Errorf("paystack: failed to build request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+p.secretKey)
	req.Header.Set("Content-Type", "application/json")

	res, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("paystack: request failed: %w", err)
	}

	defer func() { _ = res.Body.Close() }()

	payload, err := io.ReadAll(res.Body)
	if err != nil {
		return fmt.Errorf("paystack: failed to read response: %w", err)
	}

	if res.StatusCode == http.StatusNotFound {
		return payment.ErrTransactionNotFound
	}

	if err := json.Unmarshal(payload, out); err != nil {
		// The body is NOT included in the error: a Paystack error response can
		// echo request fields, and this string reaches the logs.
		return fmt.Errorf("paystack: failed to parse response (status %d): %w", res.StatusCode, err)
	}

	return nil
}
