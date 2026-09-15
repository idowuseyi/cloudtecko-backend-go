package local

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/aarondl/null/v8"
	"github.com/aarondl/sqlboiler/v4/boil"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/api/httperrors"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/data/dto"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/models"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/payment"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util/db"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util/hashing"
)

// StartRecurringDonation begins a subscription against a donation tier.
//
// The amount comes from the TIER, not the request. Paystack fixes an amount to a
// Plan, so a recurring donation cannot be an arbitrary number the way a one-off
// can -- and accepting one from the client would silently disagree with what the
// provider actually charges.
func (s *Service) StartRecurringDonation(ctx context.Context, request dto.StartRecurringDonationRequest) (dto.StartDonationResult, error) {
	log := util.LogFromContext(ctx)

	tier, err := models.DonationTiers(
		models.DonationTierWhere.ID.EQ(request.TierID),
		models.DonationTierWhere.ArchivedAt.IsNull(),
	).One(ctx, s.db)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return dto.StartDonationResult{}, httperrors.ErrNotFoundDonationTier
		}

		return dto.StartDonationResult{}, fmt.Errorf("failed to load donation tier: %w", err)
	}

	if tier.Interval == "" {
		return dto.StartDonationResult{}, httperrors.ErrBadRequestTierNotRecurring
	}

	planCode, err := s.ensurePlan(ctx, tier)
	if err != nil {
		return dto.StartDonationResult{}, err
	}

	reference, err := newPaymentReference("sub")
	if err != nil {
		return dto.StartDonationResult{}, err
	}

	accessTokenPlain, accessTokenHash, err := hashing.NewToken()
	if err != nil {
		return dto.StartDonationResult{}, fmt.Errorf("failed to generate donation access token: %w", err)
	}

	if err := db.WithTransaction(ctx, s.db, func(exec boil.ContextExecutor) error {
		donation := &models.Donation{
			Reference:       reference,
			Kind:            models.DonationKindRecurring,
			Status:          models.DonationStatusPending,
			DonationTierID:  null.StringFrom(tier.ID),
			UserID:          request.UserID,
			Email:           request.Email,
			DisplayName:     request.DisplayName,
			IsAnonymous:     request.IsAnonymous,
			AmountKobo:      tier.AmountKobo,
			AccessTokenHash: null.BytesFrom(accessTokenHash),
		}

		if err := donation.Insert(ctx, exec, boil.Infer()); err != nil {
			return fmt.Errorf("failed to insert donation: %w", err)
		}

		subscription := &models.DonationSubscription{
			DonationID: donation.ID,
			Provider:   s.paymentProviderName,
		}

		if err := subscription.Insert(ctx, exec, boil.Infer()); err != nil {
			return fmt.Errorf("failed to insert subscription: %w", err)
		}

		paymentRow := &models.Payment{
			Provider:   s.paymentProviderName,
			Status:     models.PaymentStatusPending,
			DonationID: null.StringFrom(donation.ID),
			Reference:  reference,
			AmountKobo: tier.AmountKobo,
			ExpiresAt:  s.clock.Now().Add(s.config.Payment.IntentValidity),
		}

		if err := paymentRow.Insert(ctx, exec, boil.Infer()); err != nil {
			return fmt.Errorf("failed to insert payment: %w", err)
		}

		return nil
	}); err != nil {
		return dto.StartDonationResult{}, err
	}

	initialized, err := s.payment.InitializeTransaction(ctx, payment.InitializeRequest{
		Reference:   reference,
		AmountMinor: tier.AmountKobo,
		Currency:    models.CurrencyCodeNGN,
		Email:       request.Email,
		CallbackURL: s.config.Payment.CallbackURL,
		Metadata:    map[string]string{"plan": planCode},
	})
	if err != nil {
		log.Err(err).Str("reference", reference).Msg("Failed to initialize recurring donation")

		return dto.StartDonationResult{}, httperrors.ErrBadGatewayPaymentProvider
	}

	if err := s.recordAuthorizationURL(ctx, reference, initialized.AuthorizationURL); err != nil {
		return dto.StartDonationResult{}, err
	}

	return dto.StartDonationResult{
		Reference:        reference,
		AuthorizationURL: initialized.AuthorizationURL,
		AccessToken:      accessTokenPlain,
		AmountKobo:       tier.AmountKobo,
	}, nil
}

// ensurePlan creates the provider Plan backing a tier, once.
//
// A Plan's amount cannot be changed once subscribers exist, so a price change
// means a NEW tier with a new plan and archiving the old one -- never an edit.
// That is why the plan code is cached on the tier rather than recreated.
func (s *Service) ensurePlan(ctx context.Context, tier *models.DonationTier) (string, error) {
	if tier.ProviderPlanCode.Valid && tier.ProviderPlanCode.String != "" {
		return tier.ProviderPlanCode.String, nil
	}

	plan, err := s.payment.CreatePlan(ctx, payment.PlanRequest{
		Name:        tier.Name,
		AmountMinor: tier.AmountKobo,
		Currency:    models.CurrencyCodeNGN,
		Interval:    tier.Interval,
	})
	if err != nil {
		return "", fmt.Errorf("failed to create provider plan: %w", err)
	}

	tier.ProviderPlanCode = null.StringFrom(plan.PlanCode)
	if _, err := tier.Update(ctx, s.db, boil.Whitelist(
		models.DonationTierColumns.ProviderPlanCode,
		models.DonationTierColumns.UpdatedAt,
	)); err != nil {
		return "", fmt.Errorf("failed to record provider plan code: %w", err)
	}

	return plan.PlanCode, nil
}

// RecordSubscriptionCreated stores what the provider returns exactly once.
//
// The email token is the reason this matters. Paystack returns it only here, and
// requires it back alongside the subscription code to cancel. Lose it and the
// donor can never stop giving through our interface -- they would have to
// contact their bank. It is therefore ENCRYPTED rather than hashed, the only
// credential in this schema treated that way.
//
// Takes exec rather than reaching for s.db: this runs INSIDE the webhook
// transaction, alongside the guarded status transition that makes the delivery
// idempotent. A write on s.db would commit independently of that guard, so a
// redelivery could roll back the transition and keep the subscription write --
// and it would borrow a second pooled connection while the outer transaction
// holds one, which deadlocks the pool under load.
func (s *Service) RecordSubscriptionCreated(ctx context.Context, exec boil.ContextExecutor, event payment.WebhookEvent) error {
	log := util.LogFromContext(ctx)

	if event.SubscriptionCode == "" {
		return nil
	}

	paymentRow, err := models.Payments(
		models.PaymentWhere.Reference.EQ(event.Transaction.Reference),
	).One(ctx, exec)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			log.Warn().Str("reference", event.Transaction.Reference).
				Msg("Subscription event referenced a payment we do not have")

			return nil
		}

		return fmt.Errorf("failed to load payment: %w", err)
	}

	if !paymentRow.DonationID.Valid {
		return nil
	}

	subscription, err := models.DonationSubscriptions(
		models.DonationSubscriptionWhere.DonationID.EQ(paymentRow.DonationID.String),
	).One(ctx, exec)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}

		return fmt.Errorf("failed to load subscription: %w", err)
	}

	sealed := ""

	if event.SubscriptionEmailToken != "" {
		sealed, err = s.secrets.Seal(event.SubscriptionEmailToken)
		if err != nil {
			return fmt.Errorf("failed to seal subscription email token: %w", err)
		}
	}

	subscription.ProviderSubscriptionCode = null.StringFrom(event.SubscriptionCode)
	subscription.ProviderEmailToken = null.NewString(sealed, sealed != "")
	subscription.ProviderCustomerCode = null.NewString(event.CustomerCode, event.CustomerCode != "")

	if _, err := subscription.Update(ctx, exec, boil.Whitelist(
		models.DonationSubscriptionColumns.ProviderSubscriptionCode,
		models.DonationSubscriptionColumns.ProviderEmailToken,
		models.DonationSubscriptionColumns.ProviderCustomerCode,
		models.DonationSubscriptionColumns.UpdatedAt,
	)); err != nil {
		return fmt.Errorf("failed to record subscription: %w", err)
	}

	return nil
}

// CancelRecurringDonation stops a subscription at the provider and locally.
//
// Requires the donor's access token, like reading status: there is no account to
// scope this to, and cancelling someone else's giving on a guessed reference
// would be a hostile act that costs the organisation money.
func (s *Service) CancelRecurringDonation(ctx context.Context, reference string, accessToken string) error {
	log := util.LogFromContext(ctx)

	donation, err := models.Donations(
		models.DonationWhere.Reference.EQ(reference),
		models.DonationWhere.AccessTokenHash.EQ(null.BytesFrom(hashing.HashToken(accessToken))),
		models.DonationWhere.Kind.EQ(models.DonationKindRecurring),
	).One(ctx, s.db)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return httperrors.ErrNotFoundDonation
		}

		return fmt.Errorf("failed to load donation: %w", err)
	}

	if donation.Status == models.DonationStatusCancelled {
		// Already stopped. Reporting success keeps cancellation idempotent, which
		// matters because a donor who is not sure it worked will click again.
		return nil
	}

	subscription, err := models.DonationSubscriptions(
		models.DonationSubscriptionWhere.DonationID.EQ(donation.ID),
	).One(ctx, s.db)
	if err != nil {
		return fmt.Errorf("failed to load subscription: %w", err)
	}

	if subscription.ProviderSubscriptionCode.Valid && subscription.ProviderEmailToken.Valid {
		emailToken, err := s.secrets.Open(subscription.ProviderEmailToken.String)
		if err != nil {
			return fmt.Errorf("failed to open subscription email token: %w", err)
		}

		if err := s.payment.DisableSubscription(ctx, subscription.ProviderSubscriptionCode.String, emailToken); err != nil {
			// Do NOT mark it cancelled locally. Showing a donor "cancelled" while
			// the provider keeps charging is the worst possible outcome here, so
			// the failure surfaces and they can retry.
			log.Err(err).Str("reference", reference).Msg("Provider refused to disable the subscription")

			return httperrors.ErrBadGatewayPaymentProvider
		}
	}

	return db.WithTransaction(ctx, s.db, func(exec boil.ContextExecutor) error {
		donation.Status = models.DonationStatusCancelled
		if _, err := donation.Update(ctx, exec, boil.Whitelist(
			models.DonationColumns.Status,
			models.DonationColumns.UpdatedAt,
		)); err != nil {
			return fmt.Errorf("failed to cancel donation: %w", err)
		}

		subscription.CancelledAt = null.TimeFrom(s.clock.Now())
		if _, err := subscription.Update(ctx, exec, boil.Whitelist(
			models.DonationSubscriptionColumns.CancelledAt,
			models.DonationSubscriptionColumns.UpdatedAt,
		)); err != nil {
			return fmt.Errorf("failed to record cancellation: %w", err)
		}

		return nil
	})
}

// MarkSubscriptionPastDue records a failed renewal.
//
// Not cancelled: the provider retries, and a donor whose card expired has not
// chosen to stop giving. Treating a failed charge as a cancellation would quietly
// end recurring revenue on a temporary problem.
//
// Takes exec for the same reason as [Service.RecordSubscriptionCreated].
func (s *Service) MarkSubscriptionPastDue(ctx context.Context, exec boil.ContextExecutor, subscriptionCode string) error {
	if subscriptionCode == "" {
		return nil
	}

	subscription, err := models.DonationSubscriptions(
		models.DonationSubscriptionWhere.ProviderSubscriptionCode.EQ(null.StringFrom(subscriptionCode)),
	).One(ctx, exec)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}

		return fmt.Errorf("failed to load subscription: %w", err)
	}

	donation, err := models.FindDonation(ctx, exec, subscription.DonationID)
	if err != nil {
		return fmt.Errorf("failed to load donation: %w", err)
	}

	if donation.Status == models.DonationStatusCancelled {
		return nil
	}

	donation.Status = models.DonationStatusPastDue
	if _, err := donation.Update(ctx, exec, boil.Whitelist(
		models.DonationColumns.Status,
		models.DonationColumns.UpdatedAt,
	)); err != nil {
		return fmt.Errorf("failed to mark donation past due: %w", err)
	}

	return nil
}

// MarkSubscriptionDisabled records that the PROVIDER ended a subscription.
//
// Takes exec for the same reason as [Service.RecordSubscriptionCreated], and
// deliberately opens no transaction of its own: the caller already holds one,
// and nesting would take a second connection from a pool the outer transaction
// is already holding one of.
func (s *Service) MarkSubscriptionDisabled(ctx context.Context, exec boil.ContextExecutor, subscriptionCode string) error {
	if subscriptionCode == "" {
		return nil
	}

	subscription, err := models.DonationSubscriptions(
		models.DonationSubscriptionWhere.ProviderSubscriptionCode.EQ(null.StringFrom(subscriptionCode)),
	).One(ctx, exec)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}

		return fmt.Errorf("failed to load subscription: %w", err)
	}

	donation, err := models.FindDonation(ctx, exec, subscription.DonationID)
	if err != nil {
		return fmt.Errorf("failed to load donation: %w", err)
	}

	donation.Status = models.DonationStatusCancelled
	if _, err := donation.Update(ctx, exec, boil.Whitelist(
		models.DonationColumns.Status,
		models.DonationColumns.UpdatedAt,
	)); err != nil {
		return fmt.Errorf("failed to cancel donation: %w", err)
	}

	if !subscription.CancelledAt.Valid {
		subscription.CancelledAt = null.TimeFrom(s.clock.Now())
		if _, err := subscription.Update(ctx, exec, boil.Whitelist(
			models.DonationSubscriptionColumns.CancelledAt,
			models.DonationSubscriptionColumns.UpdatedAt,
		)); err != nil {
			return fmt.Errorf("failed to record cancellation: %w", err)
		}
	}

	return nil
}
