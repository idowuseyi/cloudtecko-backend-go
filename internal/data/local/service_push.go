package local

import (
	"context"
	"database/sql"
	"errors"

	"github.com/aarondl/sqlboiler/v4/boil"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/api/httperrors"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/data/dto"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/models"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util/db"
)

func (s *Service) UpdatePushToken(ctx context.Context, request dto.UpdatePushTokenRequest) error {
	log := util.LogFromContext(ctx).With().Str("userID", request.User.ID).Logger()

	// Registering the new token and retiring the old one are deliberately TWO
	// transactions, not one. A device that presents an unknown old token still
	// has a valid new one, and refusing to store it would silently stop push
	// delivery to that device -- so the new token is committed and the missing
	// old one is reported afterwards. ErrNotFoundOldPushToken says exactly that
	// ("The new token was saved.") and TestPutUpdatePushTokenWithOldTokenNotfound
	// asserts it.
	//
	// This was previously expressed as a single WithTransaction whose insert ran
	// on s.db instead of the transaction's executor, which produced the same
	// outcome by escaping the rollback. Splitting it states the intent instead of
	// depending on which connection a call happens to use -- and stops the file,
	// which is the only domain-service example in the repo, from teaching that
	// s.db inside a transaction closure is acceptable.
	if err := db.WithTransaction(ctx, s.db, func(exec boil.ContextExecutor) error {
		tokenExists, err := models.PushTokens(
			models.PushTokenWhere.Token.EQ(request.Token),
		).Exists(ctx, exec)
		if err != nil {
			log.Err(err).Msg("Failed to check if token exists")
			return err
		}

		if tokenExists {
			log.Debug().Msg("Token already exists")
			return httperrors.ErrConflictPushToken
		}

		newToken := models.PushToken{
			UserID:   request.User.ID,
			Token:    request.Token,
			Provider: request.Provider,
		}

		if err := newToken.Insert(ctx, exec, boil.Infer()); err != nil {
			log.Err(err).Msg("Failed to insert new token")
			return err
		}

		return nil
	}); err != nil {
		log.Debug().Err(err).Msg("Failed to register new push token")
		return err
	}

	if request.ExistingToken.IsZero() {
		return nil
	}

	if err := db.WithTransaction(ctx, s.db, func(exec boil.ContextExecutor) error {
		existingToken, err := models.PushTokens(
			models.PushTokenWhere.Token.EQ(request.ExistingToken.String),
			models.PushTokenWhere.UserID.EQ(request.User.ID),
		).One(ctx, exec)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				log.Debug().Msg("Existing token not found")
				return httperrors.ErrNotFoundOldPushToken
			}

			log.Err(err).Msg("Failed to find existing token")
			return err
		}

		if _, err := existingToken.Delete(ctx, exec); err != nil {
			log.Err(err).Msg("Failed to delete existing token")
			return err
		}

		return nil
	}); err != nil {
		log.Debug().Err(err).Msg("Failed to retire old push token")
		return err
	}

	return nil
}
