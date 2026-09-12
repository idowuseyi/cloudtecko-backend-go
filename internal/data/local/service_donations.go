package local

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/aarondl/null/v8"
	"github.com/aarondl/sqlboiler/v4/boil"
	"github.com/aarondl/sqlboiler/v4/queries/qm"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/api/httperrors"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/data/dto"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/models"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/payment"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util/db"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util/hashing"
)

// ListDonationTiers returns the suggested amounts shown on the donate page.
func (s *Service) ListDonationTiers(ctx context.Context) (dto.DonationTierList, error) {
	tiers, err := models.DonationTiers(
		models.DonationTierWhere.ArchivedAt.IsNull(),
		qm.OrderBy(models.DonationTierColumns.SortOrder+", "+models.DonationTierColumns.AmountKobo),
	).All(ctx, s.db)
	if err != nil {
		return nil, fmt.Errorf("failed to list donation tiers: %w", err)
	}

	result := make(dto.DonationTierList, len(tiers))
	for i, tier := range tiers {
		result[i] = dto.DonationTier{
			ID:          tier.ID,
			Name:        tier.Name,
			Description: tier.Description,
			AmountKobo:  tier.AmountKobo,
			Interval:    tier.Interval,
		}
	}

	return result, nil
}

// StartDonation creates a donation and its payment intent, then asks the
// provider where to send the payer.
//
// The amount is taken from the REQUEST here, unlike merch where it is looked up
// per line -- a donation is genuinely whatever the donor chooses. It is still
// bounded, because an unbounded integer from an anonymous caller ends up in a
// provider API call and in our own arithmetic.
func (s *Service) StartDonation(ctx context.Context, request dto.StartDonationRequest) (dto.StartDonationResult, error) {
	log := util.LogFromContext(ctx)

	if request.AmountKobo < s.config.Payment.MinDonationKobo || request.AmountKobo > s.config.Payment.MaxDonationKobo {
		return dto.StartDonationResult{}, httperrors.ErrBadRequestDonationAmount
	}

	reference, err := newPaymentReference("don")
	if err != nil {
		return dto.StartDonationResult{}, err
	}

	accessTokenPlain, accessTokenHash, err := hashing.NewToken()
	if err != nil {
		return dto.StartDonationResult{}, fmt.Errorf("failed to generate donation access token: %w", err)
	}

	var donationID string

	if err := db.WithTransaction(ctx, s.db, func(exec boil.ContextExecutor) error {
		donation := &models.Donation{
			Reference:       reference,
			Kind:            models.DonationKindOneOff,
			Status:          models.DonationStatusPending,
			UserID:          request.UserID,
			Email:           request.Email,
			DisplayName:     request.DisplayName,
			IsAnonymous:     request.IsAnonymous,
			AmountKobo:      request.AmountKobo,
			AccessTokenHash: null.BytesFrom(accessTokenHash),
		}

		if err := donation.Insert(ctx, exec, boil.Infer()); err != nil {
			log.Err(err).Msg("Failed to insert donation")

			return fmt.Errorf("failed to insert donation: %w", err)
		}

		donationID = donation.ID

		paymentRow := &models.Payment{
			Provider:   s.paymentProviderName,
			Status:     models.PaymentStatusPending,
			DonationID: null.StringFrom(donation.ID),
			Reference:  reference,
			AmountKobo: request.AmountKobo,
			ExpiresAt:  s.clock.Now().Add(s.config.Payment.IntentValidity),
		}

		if err := paymentRow.Insert(ctx, exec, boil.Infer()); err != nil {
			log.Err(err).Msg("Failed to insert payment")

			return fmt.Errorf("failed to insert payment: %w", err)
		}

		return nil
	}); err != nil {
		return dto.StartDonationResult{}, err
	}

	// The provider call happens AFTER the commit, deliberately. Holding a
	// transaction open across a network round trip to a third party would pin a
	// connection for as long as that party takes to answer. The cost is that a
	// failure here leaves a pending donation with no authorization URL -- which
	// is harmless: no money moved, and the intent expires on its own.
	initialized, err := s.payment.InitializeTransaction(ctx, payment.InitializeRequest{
		Reference:   reference,
		AmountMinor: request.AmountKobo,
		Currency:    models.CurrencyCodeNGN,
		Email:       request.Email,
		CallbackURL: s.config.Payment.CallbackURL,
		Metadata:    map[string]string{"donation_id": donationID},
	})
	if err != nil {
		log.Err(err).Str("reference", reference).Msg("Failed to initialize payment with provider")

		return dto.StartDonationResult{}, httperrors.ErrBadGatewayPaymentProvider
	}

	if err := s.recordAuthorizationURL(ctx, reference, initialized.AuthorizationURL); err != nil {
		return dto.StartDonationResult{}, err
	}

	return dto.StartDonationResult{
		Reference:        reference,
		AuthorizationURL: initialized.AuthorizationURL,
		AccessToken:      accessTokenPlain,
		AmountKobo:       request.AmountKobo,
	}, nil
}

func (s *Service) recordAuthorizationURL(ctx context.Context, reference string, authorizationURL string) error {
	paymentRow, err := models.Payments(models.PaymentWhere.Reference.EQ(reference)).One(ctx, s.db)
	if err != nil {
		return fmt.Errorf("failed to load payment: %w", err)
	}

	paymentRow.AuthorizationURL = null.StringFrom(authorizationURL)

	if _, err := paymentRow.Update(ctx, s.db, boil.Whitelist(
		models.PaymentColumns.AuthorizationURL,
		models.PaymentColumns.UpdatedAt,
	)); err != nil {
		return fmt.Errorf("failed to record authorization url: %w", err)
	}

	return nil
}

// GetDonationStatus reports a donation's state to its donor, and settles it if
// the provider says it succeeded but no webhook has arrived.
//
// The caller must present the access token issued at checkout. Looking up by
// reference alone would let anyone holding or guessing a reference read someone
// else's donation -- and the reference travels in a callback URL.
func (s *Service) GetDonationStatus(ctx context.Context, reference string, accessToken string) (dto.DonationStatus, error) {
	log := util.LogFromContext(ctx)

	donation, err := models.Donations(
		models.DonationWhere.Reference.EQ(reference),
		models.DonationWhere.AccessTokenHash.EQ(null.BytesFrom(hashing.HashToken(accessToken))),
	).One(ctx, s.db)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// A wrong token and an unknown reference are reported identically, so
			// the endpoint cannot be used to discover which references exist.
			return dto.DonationStatus{}, httperrors.ErrNotFoundDonation
		}

		return dto.DonationStatus{}, fmt.Errorf("failed to load donation: %w", err)
	}

	if donation.Status == models.DonationStatusPending {
		if err := s.reconcileWithProvider(ctx, reference); err != nil {
			// Reconciliation is best-effort: the donor still gets the state we
			// hold, and the webhook remains the primary path.
			log.Debug().Err(err).Str("reference", reference).Msg("Provider reconciliation failed")
		}

		if err := donation.Reload(ctx, s.db); err != nil {
			return dto.DonationStatus{}, fmt.Errorf("failed to reload donation: %w", err)
		}
	}

	return dto.DonationStatus{
		Reference:  donation.Reference,
		Status:     donation.Status,
		AmountKobo: donation.AmountKobo,
		Currency:   donation.Currency,
	}, nil
}

// reconcileWithProvider asks the provider what actually happened and applies it
// through the same guarded settlement the webhook uses.
//
// This is what makes a dropped or delayed webhook survivable. It NEVER trusts
// the redirect itself: the callback URL is fully attacker-controlled, so the
// only thing that counts is what the provider says server-to-server.
func (s *Service) reconcileWithProvider(ctx context.Context, reference string) error {
	transaction, err := s.payment.VerifyTransaction(ctx, reference)
	if err != nil {
		if errors.Is(err, payment.ErrTransactionNotFound) {
			return nil
		}

		return fmt.Errorf("failed to verify transaction: %w", err)
	}

	if transaction.Status != payment.StatusSucceeded {
		return nil
	}

	return db.WithTransaction(ctx, s.db, func(exec boil.ContextExecutor) error {
		_, err := s.settleSucceededTransaction(ctx, exec, transaction)

		return err
	})
}

// ListDonations returns donations for admin review.
func (s *Service) ListDonations(ctx context.Context, status null.String) (dto.DonationList, error) {
	mods := []qm.QueryMod{qm.OrderBy(models.DonationColumns.CreatedAt + " DESC")}
	if status.Valid && status.String != "" {
		mods = append(mods, models.DonationWhere.Status.EQ(status.String))
	}

	donations, err := models.Donations(mods...).All(ctx, s.db)
	if err != nil {
		return nil, fmt.Errorf("failed to list donations: %w", err)
	}

	result := make(dto.DonationList, len(donations))
	for i, donation := range donations {
		result[i] = dto.Donation{
			ID:          donation.ID,
			Reference:   donation.Reference,
			Status:      donation.Status,
			AmountKobo:  donation.AmountKobo,
			Currency:    donation.Currency,
			Email:       donation.Email,
			DisplayName: donation.DisplayName,
			IsAnonymous: donation.IsAnonymous,
			CreatedAt:   donation.CreatedAt,
		}
	}

	return result, nil
}
