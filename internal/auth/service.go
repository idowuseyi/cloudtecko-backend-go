package auth

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/aarondl/null/v8"
	"github.com/aarondl/sqlboiler/v4/boil"
	"github.com/aarondl/sqlboiler/v4/queries/qm"
	"github.com/dropbox/godropbox/time2"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/api/httperrors"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/config"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/data/dto"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/data/mapper"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/models"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util/db"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util/hashing"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util/password"
	"github.com/labstack/echo/v4"
)

type Service struct {
	config config.Server
	db     *sql.DB
	clock  time2.Clock
}

func NewService(config config.Server, db *sql.DB, clock time2.Clock) *Service {
	return &Service{
		config: config,
		db:     db,
		clock:  clock,
	}
}

func (s *Service) GetAppUserProfile(ctx context.Context, userID string) (*dto.AppUserProfile, error) {
	log := util.LogFromContext(ctx).With().Str("userID", userID).Logger()

	aup, err := models.AppUserProfiles(
		models.AppUserProfileWhere.UserID.EQ(userID),
	).One(ctx, s.db)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			log.Debug().Err(err).Msg("AppUserProfile not found")
			return nil, ErrNotFound
		}

		log.Err(err).Msg("Failed to get AppUserProfile")
		return nil, err
	}

	return mapper.LocalAppUserProfileToDTO(aup).Ptr(), nil
}

func (s *Service) UpdatePassword(ctx context.Context, request dto.UpdatePasswordRequest) (dto.LoginResult, error) {
	log := util.LogFromContext(ctx).With().Str("userID", request.User.ID).Logger()

	if !request.User.IsActive {
		log.Debug().Msg("User is deactivated, rejecting password change")
		return dto.LoginResult{}, httperrors.ErrForbiddenUserDeactivated
	}

	if !request.User.PasswordHash.Valid {
		log.Debug().Msg("Failed to update user password, user is missing password")
		return dto.LoginResult{}, httperrors.ErrForbiddenNotLocalUser
	}

	if !request.SkipCurrentPasswordVerification {
		match, err := hashing.ComparePasswordAndHash(request.CurrentPassword, request.User.PasswordHash.String)
		if err != nil {
			log.Err(err).Msg("Failed to compare password with stored hash")
			return dto.LoginResult{}, err
		}

		if !match {
			log.Debug().Msg("Failed to update user password, provided password does not match stored hash")
			return dto.LoginResult{}, echo.ErrUnauthorized
		}
	}

	// Both change-password and reset-password funnel through here, so the policy
	// is enforced once for both.
	if err := password.Validate(request.NewPassword, s.passwordPolicy(request.User.Username.String)); err != nil {
		// Logged at debug with the reason, returned to the caller without it: the
		// specific rule a candidate broke is a hint to anyone probing the policy.
		log.Debug().Err(err).Msg("Rejected new password: does not meet the password policy")
		return dto.LoginResult{}, httperrors.ErrBadRequestInvalidPassword
	}

	hash, err := hashing.HashPassword(request.NewPassword, hashing.DefaultArgon2ParamsFromEnv())
	if err != nil {
		log.Err(err).Msg("Failed to hash new password")
		return dto.LoginResult{}, httperrors.ErrBadRequestInvalidPassword
	}

	var result dto.LoginResult
	if err := db.WithTransaction(ctx, s.db, func(exec boil.ContextExecutor) error {
		request.User.PasswordHash = null.StringFrom(hash)

		user := request.User.ToModels()

		if _, err := user.Update(ctx, exec, boil.Whitelist(models.UserColumns.Password, models.UserColumns.UpdatedAt)); err != nil {
			log.Err(err).Msg("Failed to update user")
			return err
		}

		result, err = s.authenticateUser(ctx, exec, dto.AuthenticateUserRequest{
			User:                     request.User,
			InvalidateExistingTokens: true,
		})
		if err != nil {
			log.Err(err).Msg("Failed to authenticate user after password change")
			return err
		}

		return nil
	}); err != nil {
		log.Debug().Err(err).Msg("Failed to change password")
		return dto.LoginResult{}, err
	}

	return result, nil
}

func (s *Service) ResetPassword(ctx context.Context, request dto.ResetPasswordRequest) (dto.LoginResult, error) {
	log := util.LogFromContext(ctx).With().Logger()

	passwordResetToken, err := models.PasswordResetTokens(
		models.PasswordResetTokenWhere.TokenHash.EQ(hashing.HashToken(request.ResetToken)),
		qm.Load(models.PasswordResetTokenRels.User),
	).One(ctx, s.db)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			log.Debug().Err(err).Msg("Password reset token not found")
			return dto.LoginResult{}, httperrors.ErrNotFoundTokenNotFound
		}

		log.Err(err).Msg("Failed to load password reset token")
		return dto.LoginResult{}, err
	}

	if s.clock.Now().After(passwordResetToken.ValidUntil) {
		log.Debug().Time("validUntil", passwordResetToken.ValidUntil).Msg("Password reset token is no longer valid, rejecting password reset")
		return dto.LoginResult{}, httperrors.ErrConflictTokenExpired
	}

	result, err := s.UpdatePassword(ctx, dto.UpdatePasswordRequest{
		User:                            mapper.LocalUserToDTO(passwordResetToken.R.User),
		NewPassword:                     request.NewPassword,
		SkipCurrentPasswordVerification: true,
	})
	if err != nil {
		return dto.LoginResult{}, err
	}

	// Consume the token. Without this the reset link stays usable for the rest of
	// its validity, so anyone who later obtains it -- from a shared mailbox,
	// browser history, or a forwarded mail -- can reset the password again. The
	// confirmation-token flow already deleted its token; this one did not.
	//
	// Deleted only after the password change succeeds, so a failed attempt does
	// not burn the user's only link.
	if _, err := passwordResetToken.Delete(ctx, s.db); err != nil {
		// The password IS already changed at this point, so this must not fail the
		// request -- it is logged for follow-up instead.
		log.Err(err).Msg("Failed to delete used password reset token")
	}

	return result, nil
}

func (s *Service) InitPasswordReset(ctx context.Context, request dto.InitPasswordResetRequest) (dto.InitPasswordResetResult, error) {
	log := util.LogFromContext(ctx).With().Str("username", request.Username.String()).Logger()

	user, err := models.Users(
		models.UserWhere.Username.EQ(null.StringFrom(request.Username.String())),
	).One(ctx, s.db)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			log.Debug().Err(err).Msg("User not found")
			return dto.InitPasswordResetResult{}, nil
		}

		log.Err(err).Err(err).Msg("Failed to load user")
		return dto.InitPasswordResetResult{}, err
	}

	if !user.IsActive {
		log.Debug().Msg("User is deactivated, skipping password reset")
		return dto.InitPasswordResetResult{}, nil
	}

	if !user.Password.Valid {
		log.Debug().Msg("User is missing password, skipping password reset")
		return dto.InitPasswordResetResult{}, nil
	}

	if s.config.Auth.PasswordResetTokenDebounceDuration > 0 {
		resetTokenInDebounceTimeExists, err := user.PasswordResetTokens(
			models.PasswordResetTokenWhere.CreatedAt.GT(s.clock.Now().Add(-s.config.Auth.PasswordResetTokenDebounceDuration)),
			models.PasswordResetTokenWhere.ValidUntil.GT(s.clock.Now()),
		).Exists(ctx, s.db)
		if err != nil {
			log.Err(err).Msg("Failed to check for existing password reset token")
			return dto.InitPasswordResetResult{}, err
		}

		if resetTokenInDebounceTimeExists {
			log.Debug().Msg("Password reset token exists within debounce time, not sending new one")
			return dto.InitPasswordResetResult{}, nil
		}
	}

	var result dto.InitPasswordResetResult
	if err := db.WithTransaction(ctx, s.db, func(exec boil.ContextExecutor) error {
		// Only the hash of a reset token is stored, so an existing token's value
		// can no longer be re-sent. Requesting a reset therefore invalidates any
		// earlier one: exactly one live reset link per user, which is both the
		// stronger option and what users expect from password reset.
		if _, err := user.PasswordResetTokens().DeleteAll(ctx, exec); err != nil {
			log.Err(err).Msg("Failed to delete existing password reset tokens")
			return err
		}

		plaintext, tokenHash, err := hashing.NewToken()
		if err != nil {
			log.Err(err).Msg("Failed to generate password reset token")
			return err
		}

		passwordResetToken := &models.PasswordResetToken{
			TokenHash:  tokenHash,
			UserID:     user.ID,
			ValidUntil: s.clock.Now().Add(s.config.Auth.PasswordResetTokenValidity),
		}

		if err := passwordResetToken.Insert(ctx, exec, boil.Infer()); err != nil {
			log.Err(err).Msg("Failed to insert password reset token")
			return err
		}

		result.ResetToken = null.StringFrom(plaintext)

		return nil
	}); err != nil {
		log.Debug().Err(err).Msg("Failed to initiate password reset")
		return dto.InitPasswordResetResult{}, err
	}

	return result, nil
}

func (s *Service) Logout(ctx context.Context, request dto.LogoutRequest) error {
	log := util.LogFromContext(ctx)

	if err := db.WithTransaction(ctx, s.db, func(exec boil.ContextExecutor) error {
		if _, err := models.AccessTokens(models.AccessTokenWhere.TokenHash.EQ(hashing.HashToken(request.AccessToken))).DeleteAll(ctx, exec); err != nil {
			log.Err(err).Msg("Failed to delete access token")
			return err
		}

		if request.RefreshToken.IsZero() {
			return nil
		}

		if _, err := models.RefreshTokens(models.RefreshTokenWhere.TokenHash.EQ(hashing.HashToken(request.RefreshToken.String))).DeleteAll(ctx, exec); err != nil {
			log.Err(err).Msg("Failed to delete refresh token")
			return err
		}

		return nil
	}); err != nil {
		log.Debug().Err(err).Msg("Failed to process logout")
		return err
	}

	return nil
}

func (s *Service) Login(ctx context.Context, request dto.LoginRequest) (dto.LoginResult, error) {
	log := util.LogFromContext(ctx)

	user, err := models.Users(
		models.UserWhere.Username.EQ(null.StringFrom(request.Username.String())),
	).One(ctx, s.db)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			log.Debug().Msg("User not found")

			// Spend the same Argon2 work we would have spent on a real user before
			// answering. Returning immediately here makes an unknown username
			// resolve in microseconds while a known one costs a full ~64MB Argon2
			// hash, which is a reliable remote oracle for "does this account exist".
			hashing.CompareDummyPasswordAndHash(request.Password)

			return dto.LoginResult{}, echo.ErrUnauthorized
		}

		// Only a genuine database failure is error-worthy; a wrong username is not.
		log.Err(err).Msg("Failed to load user")

		return dto.LoginResult{}, echo.ErrUnauthorized
	}

	if !user.IsActive {
		log.Debug().Msg("User is deactivated, rejecting authentication")
		return dto.LoginResult{}, httperrors.ErrForbiddenUserDeactivated
	}

	if !user.Password.Valid {
		log.Debug().Msg("User is missing password, forbidding authentication")
		return dto.LoginResult{}, echo.ErrUnauthorized
	}

	// Per-account throttling. The IP rate limiter does not cover this: an attacker
	// spreading attempts across addresses hits it never, while hammering one
	// account. Still Argon2-hash first, so a throttled account does not answer
	// faster than a live one and become an enumeration oracle.
	if s.loginThrottled(user) {
		hashing.CompareDummyPasswordAndHash(request.Password)

		log.Warn().
			Str("userID", user.ID).
			Int("failedAttempts", user.FailedLoginAttempts).
			Msg("Login throttled: too many consecutive failed attempts")

		// Deliberately the same 401 as a wrong password. A distinct status would
		// tell an attacker the account exists and that they are close enough to
		// have triggered the throttle.
		//
		// Note this returns BEFORE recordFailedLogin, so a throttled attempt does
		// not extend the window. Otherwise an attacker could hold a user out
		// indefinitely just by continuing to try, turning the protection into the
		// denial-of-service it exists to avoid.
		return dto.LoginResult{}, echo.ErrUnauthorized
	}

	match, err := hashing.ComparePasswordAndHash(request.Password, user.Password.String)
	if err != nil {
		log.Debug().Err(err).Msg("Failed to compare password with stored hash")
		return dto.LoginResult{}, echo.ErrUnauthorized
	}

	if !match {
		log.Debug().Msg("Provided password does not match stored hash")
		s.recordFailedLogin(ctx, user)

		return dto.LoginResult{}, echo.ErrUnauthorized
	}

	s.resetFailedLogins(ctx, user)

	var result dto.LoginResult
	err = db.WithTransaction(ctx, s.db, func(exec boil.ContextExecutor) error {
		var err error
		result, err = s.authenticateUser(ctx, exec, dto.AuthenticateUserRequest{
			User: mapper.LocalUserToDTO(user),
		})
		if err != nil {
			log.Err(err).Msg("Failed to authenticate user")
			return err
		}

		return nil
	})
	if err != nil {
		log.Debug().Err(err).Msg("Failed to authenticate user")
		return dto.LoginResult{}, err
	}

	return result, nil
}

func (s *Service) Refresh(ctx context.Context, request dto.RefreshRequest) (dto.LoginResult, error) {
	log := util.LogFromContext(ctx)

	oldRefreshToken, err := models.RefreshTokens(
		models.RefreshTokenWhere.TokenHash.EQ(hashing.HashToken(request.RefreshToken)),
		qm.Load(models.RefreshTokenRels.User),
	).One(ctx, s.db)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			log.Debug().Msg("Refresh token not found")
			return dto.LoginResult{}, echo.ErrUnauthorized
		}

		log.Err(err).Msg("Failed to load refresh token")
		return dto.LoginResult{}, err
	}

	// Replay of an already-rotated token means the chain leaked: either the
	// attacker is using a stolen token or the legitimate client is racing one.
	// Either way we cannot tell them apart, so revoke the whole family and force
	// a real login. Consumed tokens are retained until they expire precisely so
	// this is detectable at all.
	if oldRefreshToken.ConsumedAt.Valid {
		log.Warn().
			Str("familyID", oldRefreshToken.FamilyID).
			Str("userID", oldRefreshToken.UserID).
			Msg("Refresh token reuse detected, revoking the entire token family")

		if err := s.revokeRefreshTokenFamily(ctx, oldRefreshToken); err != nil {
			log.Err(err).Msg("Failed to revoke reused refresh token family")
			return dto.LoginResult{}, err
		}

		return dto.LoginResult{}, echo.ErrUnauthorized
	}

	if s.clock.Now().After(oldRefreshToken.ValidUntil) {
		log.Debug().Msg("Refresh token has expired")
		return dto.LoginResult{}, echo.ErrUnauthorized
	}

	user := oldRefreshToken.R.User

	if !user.IsActive {
		log.Debug().Msg("User is deactivated, rejecting token refresh")
		return dto.LoginResult{}, httperrors.ErrForbiddenUserDeactivated
	}

	var result dto.LoginResult
	err = db.WithTransaction(ctx, s.db, func(exec boil.ContextExecutor) error {
		// Mark consumed rather than delete: a deleted row is indistinguishable
		// from a token that never existed, which is what made replay invisible.
		oldRefreshToken.ConsumedAt = null.TimeFrom(s.clock.Now())

		if _, err := oldRefreshToken.Update(ctx, exec, boil.Whitelist(
			models.RefreshTokenColumns.ConsumedAt,
			models.RefreshTokenColumns.UpdatedAt,
		)); err != nil {
			log.Err(err).Msg("Failed to mark old refresh token as consumed")
			return err
		}

		result, err = s.authenticateUser(ctx, exec, dto.AuthenticateUserRequest{
			User:                     mapper.LocalUserToDTO(user),
			InvalidateExistingTokens: false,
			RefreshTokenFamilyID:     oldRefreshToken.FamilyID,
		})
		if err != nil {
			log.Err(err).Msg("Failed to authenticate user")
			return err
		}

		return nil
	})
	if err != nil {
		log.Debug().Err(err).Msg("Failed to refresh token")
		return dto.LoginResult{}, err
	}

	return result, nil
}

func (s *Service) Register(ctx context.Context, request dto.RegisterRequest) (dto.RegisterResult, error) {
	log := util.LogFromContext(ctx).With().Str("username", request.Username.String()).Logger()

	user, err := models.Users(
		models.UserWhere.Username.EQ(null.StringFrom(request.Username.String())),
	).One(ctx, s.db)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		log.Err(err).Msg("Failed to check whether user exists")
		return dto.RegisterResult{}, err
	}

	// An existing unconfirmed user re-registering is a resend, not a conflict.
	if user != nil {
		return s.resendConfirmation(ctx, user)
	}

	if err := password.Validate(request.Password, s.passwordPolicy(request.Username.String())); err != nil {
		log.Debug().Err(err).Msg("Rejected registration password: does not meet the password policy")
		return dto.RegisterResult{}, httperrors.ErrBadRequestInvalidPassword
	}

	hash, err := hashing.HashPassword(request.Password, hashing.DefaultArgon2ParamsFromEnv())
	if err != nil {
		log.Err(err).Msg("Failed to hash user password")
		return dto.RegisterResult{}, httperrors.ErrBadRequestInvalidPassword
	}

	result := dto.RegisterResult{
		RequiresConfirmation: s.config.Auth.RegistrationRequiresConfirmation,
	}

	if err := db.WithTransaction(ctx, s.db, func(exec boil.ContextExecutor) error {
		user := &models.User{
			Username:             null.StringFrom(request.Username.String()),
			Password:             null.StringFrom(hash),
			LastAuthenticatedAt:  null.TimeFrom(s.clock.Now()),
			IsActive:             !result.RequiresConfirmation,
			RequiresConfirmation: result.RequiresConfirmation,
			Scopes:               s.config.Auth.DefaultUserScopes,
		}

		if err := user.Insert(ctx, exec, boil.Infer()); err != nil {
			log.Err(err).Msg("Failed to insert user")
			return err
		}

		appUserProfile := models.AppUserProfile{
			UserID: user.ID,
		}

		if err := appUserProfile.Insert(ctx, exec, boil.Infer()); err != nil {
			log.Err(err).Msg("Failed to insert app user profile")
			return err
		}

		if result.RequiresConfirmation {
			plaintext, tokenHash, err := hashing.NewToken()
			if err != nil {
				log.Err(err).Msg("Failed to generate confirmation token")
				return err
			}

			confirmationToken := models.ConfirmationToken{
				TokenHash:  tokenHash,
				UserID:     user.ID,
				ValidUntil: s.clock.Now().Add(s.config.Auth.ConfirmationTokenValidity),
			}

			if err := confirmationToken.Insert(ctx, exec, boil.Infer()); err != nil {
				log.Err(err).Msg("Failed to insert confirmation token")
				return err
			}

			result.ConfirmationToken = null.StringFrom(plaintext)
		}

		return nil
	}); err != nil {
		log.Debug().Err(err).Msg("Failed to register user")
		return dto.RegisterResult{}, err
	}

	return result, nil
}

func (s *Service) DeleteUserAccount(ctx context.Context, request dto.DeleteUserAccountRequest) error {
	log := util.LogFromContext(ctx)

	if !request.User.IsActive {
		log.Debug().Msg("User is deactivated, rejecting deletion")
		return httperrors.ErrForbiddenUserDeactivated
	}

	if !request.User.PasswordHash.Valid {
		log.Debug().Msg("Failed to delete user account, user is missing password")
		return httperrors.ErrForbiddenNotLocalUser
	}

	match, err := hashing.ComparePasswordAndHash(request.CurrentPassword, request.User.PasswordHash.String)
	if err != nil {
		log.Debug().Err(err).Msg("Failed to compare password with stored hash")
		return echo.ErrUnauthorized
	}

	if !match {
		log.Debug().Msg("Provided password does not match stored hash")
		return echo.ErrUnauthorized
	}

	// delete the user and all related data
	err = db.WithTransaction(ctx, s.db, func(exec boil.ContextExecutor) error {
		_, err = models.Users(
			models.UserWhere.ID.EQ(request.User.ID),
		).DeleteAll(ctx, exec)
		if err != nil {
			log.Err(err).Msg("Failed to delete user")
			return err
		}

		return nil
	})
	if err != nil {
		log.Debug().Err(err).Msg("Failed to delete user account")
		return err
	}

	return nil
}

func (s *Service) CompleteRegister(ctx context.Context, request dto.CompleteRegisterRequest) (dto.LoginResult, error) {
	log := util.LogFromContext(ctx)

	var result dto.LoginResult
	err := db.WithTransaction(ctx, s.db, func(exec boil.ContextExecutor) error {
		// exec, not s.db: reading on the pool instead of the transaction sees a
		// snapshot taken outside it and takes no row lock, so two concurrent
		// requests carrying the same token both find it live. FOR UPDATE then
		// serialises them — the second blocks until the first commits and then
		// finds the row deleted, rather than both activating the user and both
		// being issued a session from one single-use token.
		confirmationToken, err := models.ConfirmationTokens(
			models.ConfirmationTokenWhere.TokenHash.EQ(hashing.HashToken(request.ConfirmationToken)),
			models.ConfirmationTokenWhere.ValidUntil.GT(s.clock.Now()),
			qm.Load(models.ConfirmationTokenRels.User),
			qm.For("UPDATE OF confirmation_tokens"),
		).One(ctx, exec)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				log.Debug().Err(err).Msg("Confirmation token not found")
				return httperrors.ErrNotFoundTokenNotFound
			}

			log.Err(err).Msg("Failed to load confirmation token")
			return err
		}

		user := confirmationToken.R.User
		if user.IsActive || !user.RequiresConfirmation {
			log.Debug().Msg("User already active, skipping confirmation")
			return nil
		}

		user.IsActive = true
		user.RequiresConfirmation = false
		if _, err := user.Update(ctx, exec, boil.Whitelist(models.UserColumns.IsActive, models.UserColumns.RequiresConfirmation, models.UserColumns.UpdatedAt)); err != nil {
			log.Err(err).Msg("Failed to update user")
			return err
		}

		if _, err := confirmationToken.Delete(ctx, exec); err != nil {
			log.Err(err).Msg("Failed to delete confirmation token")
			return err
		}

		result, err = s.authenticateUser(ctx, exec, dto.AuthenticateUserRequest{
			User: mapper.LocalUserToDTO(confirmationToken.R.User),
		})
		if err != nil {
			log.Err(err).Msg("Failed to authenticate user")
			return err
		}

		return nil
	})
	if err != nil {
		log.Debug().Err(err).Msg("Failed to complete registration")
		return dto.LoginResult{}, err
	}

	return result, nil
}

func (s *Service) authenticateUser(ctx context.Context, exec boil.ContextExecutor, request dto.AuthenticateUserRequest) (dto.LoginResult, error) {
	log := util.LogFromContext(ctx)

	result := dto.LoginResult{
		TokenType: TokenTypeBearer,
		ExpiresIn: int64(s.config.Auth.AccessTokenValidity.Seconds()),
	}

	if request.InvalidateExistingTokens {
		if _, err := models.AccessTokens(
			models.AccessTokenWhere.UserID.EQ(request.User.ID),
		).DeleteAll(ctx, exec); err != nil {
			log.Err(err).Msg("Failed to delete existing access tokens")
			return dto.LoginResult{}, err
		}

		if _, err := models.RefreshTokens(
			models.RefreshTokenWhere.UserID.EQ(request.User.ID),
		).DeleteAll(ctx, exec); err != nil {
			log.Err(err).Msg("Failed to delete existing refresh tokens")
			return dto.LoginResult{}, err
		}
	}

	// Tokens are generated here rather than by a uuid_generate_v4() column
	// default, because only the hash is persisted -- the database never sees the
	// value the client is given.
	accessTokenPlain, accessTokenHash, err := hashing.NewToken()
	if err != nil {
		log.Err(err).Msg("Failed to generate access token")
		return dto.LoginResult{}, err
	}

	refreshTokenPlain, refreshTokenHash, err := hashing.NewToken()
	if err != nil {
		log.Err(err).Msg("Failed to generate refresh token")
		return dto.LoginResult{}, err
	}

	accessToken := models.AccessToken{
		TokenHash:  accessTokenHash,
		ValidUntil: s.clock.Now().Add(s.config.Auth.AccessTokenValidity),
		UserID:     request.User.ID,
	}

	if err := accessToken.Insert(ctx, exec, boil.Infer()); err != nil {
		log.Err(err).Msg("Failed to insert access token")
		return dto.LoginResult{}, err
	}

	refreshToken := models.RefreshToken{
		TokenHash:  refreshTokenHash,
		ValidUntil: s.clock.Now().Add(s.config.Auth.RefreshTokenValidity),
		UserID:     request.User.ID,
	}

	// A rotated token stays in the family it came from, so that replay of any
	// link in the chain can revoke the whole chain. A fresh login starts a new
	// family, which the column default provides.
	if request.RefreshTokenFamilyID != "" {
		refreshToken.FamilyID = request.RefreshTokenFamilyID
	}

	if err := refreshToken.Insert(ctx, exec, boil.Infer()); err != nil {
		log.Err(err).Msg("Failed to insert refresh token")
		return dto.LoginResult{}, err
	}

	u := request.User.ToModels()
	u.LastAuthenticatedAt = null.TimeFrom(s.clock.Now())

	if _, err := u.Update(ctx, exec, boil.Whitelist(models.UserColumns.LastAuthenticatedAt, models.UserColumns.UpdatedAt)); err != nil {
		log.Err(err).Msg("Failed to update user last authenticated time")
		return dto.LoginResult{}, err
	}

	result.AccessToken = accessTokenPlain
	result.RefreshToken = refreshTokenPlain

	return result, nil
}

// revokeRefreshTokenFamily deletes every refresh token in a leaked chain and all
// of that user's access tokens, so a detected replay ends every session derived
// from it rather than only the token that was presented.
func (s *Service) revokeRefreshTokenFamily(ctx context.Context, token *models.RefreshToken) error {
	return db.WithTransaction(ctx, s.db, func(exec boil.ContextExecutor) error {
		if _, err := models.RefreshTokens(
			models.RefreshTokenWhere.FamilyID.EQ(token.FamilyID),
		).DeleteAll(ctx, exec); err != nil {
			return err
		}

		if _, err := models.AccessTokens(
			models.AccessTokenWhere.UserID.EQ(token.UserID),
		).DeleteAll(ctx, exec); err != nil {
			return err
		}

		return nil
	})
}

// resendConfirmation handles a registration attempt for a username that already
// exists but has never been confirmed: it re-issues a confirmation token unless
// the debounce window says one was sent recently.
func (s *Service) resendConfirmation(ctx context.Context, user *models.User) (dto.RegisterResult, error) {
	log := util.LogFromContext(ctx).With().Str("username", user.Username.String).Logger()

	if !user.RequiresConfirmation {
		// S-020: returning a 409 here made /register an account-enumeration
		// oracle -- anyone could test whether an address had signed up. The
		// response is now the same one a brand-new registration produces, and the
		// difference is delivered by email to the MAILBOX OWNER instead of to the
		// person making the request.
		//
		// Caveat worth knowing: this only fully closes the oracle when
		// RegistrationRequiresConfirmation is true. With confirmation disabled a
		// genuinely new registration is answered with a session, which an existing
		// address cannot be given, and that difference is itself the signal. The
		// server logs a warning at startup in that configuration.
		log.Debug().Msg("Registration attempted for an address that already has an account")

		return dto.RegisterResult{
			RequiresConfirmation: true,
			AccountAlreadyExists: true,
		}, nil
	}

	confirmationTokenInDebounceTimeExists, err := user.ConfirmationTokens(
		models.ConfirmationTokenWhere.CreatedAt.GT(s.clock.Now().Add(-s.config.Auth.ConfirmationTokenDebounceDuration)),
		models.ConfirmationTokenWhere.ValidUntil.GT(s.clock.Now()),
	).Exists(ctx, s.db)
	if err != nil {
		log.Err(err).Msg("Failed to check for existing confirmation token")
		return dto.RegisterResult{}, err
	}

	if confirmationTokenInDebounceTimeExists {
		return dto.RegisterResult{
			RequiresConfirmation: user.RequiresConfirmation,
		}, nil
	}

	plaintext, tokenHash, err := hashing.NewToken()
	if err != nil {
		log.Err(err).Msg("Failed to generate confirmation token")
		return dto.RegisterResult{}, err
	}

	confirmationToken := models.ConfirmationToken{
		TokenHash:  tokenHash,
		UserID:     user.ID,
		ValidUntil: s.clock.Now().Add(s.config.Auth.ConfirmationTokenValidity),
	}

	if err := confirmationToken.Insert(ctx, s.db, boil.Infer()); err != nil {
		log.Err(err).Msg("Failed to insert confirmation token")
		return dto.RegisterResult{}, err
	}

	return dto.RegisterResult{
		RequiresConfirmation: user.RequiresConfirmation,
		ConfirmationToken:    null.StringFrom(plaintext),
	}, nil
}

// passwordPolicy builds the rules for SETTING a password, including the
// context-specific values this particular user must not use.
//
// Never applied when verifying: rejecting input before the credential check
// would lock out a user whose password predates the policy.
func (s *Service) passwordPolicy(username string) password.Policy {
	identifiers := append([]string{}, s.config.Auth.PasswordBlockedTerms...)

	if username != "" {
		identifiers = append(identifiers, username)

		// The local part on its own, since "jane@example.com" -> "janejanejane"
		// is the obvious thing to try.
		if local, _, found := strings.Cut(username, "@"); found && local != "" {
			identifiers = append(identifiers, local)
		}
	}

	return password.Policy{
		MinLength:   s.config.Auth.PasswordMinLength,
		MaxLength:   s.config.Auth.PasswordMaxLength,
		Identifiers: identifiers,
	}
}

// loginThrottled reports whether an account has had too many consecutive failed
// logins too recently.
//
// The window is deliberately short and the threshold generous. A hard lockout
// would hand an attacker a denial-of-service primitive -- anyone knowing a
// username could keep that user permanently out -- so this bounds the damage to
// minutes while still cutting brute-force throughput by orders of magnitude.
func (s *Service) loginThrottled(user *models.User) bool {
	if s.config.Auth.LoginMaxFailedAttempts <= 0 {
		return false
	}

	if user.FailedLoginAttempts < s.config.Auth.LoginMaxFailedAttempts {
		return false
	}

	if !user.LastFailedLoginAt.Valid {
		return false
	}

	return s.clock.Now().Before(user.LastFailedLoginAt.Time.Add(s.config.Auth.LoginFailureWindow))
}

// recordFailedLogin increments the consecutive-failure counter.
//
// Best-effort: a bookkeeping failure must not turn a wrong password into a 500,
// which would itself distinguish this path from a normal rejection.
func (s *Service) recordFailedLogin(ctx context.Context, user *models.User) {
	log := util.LogFromContext(ctx)

	// Restart the count when the previous failure has aged out, so unrelated
	// mistakes months apart do not accumulate into a lockout.
	if user.LastFailedLoginAt.Valid && s.clock.Now().After(user.LastFailedLoginAt.Time.Add(s.config.Auth.LoginFailureWindow)) {
		user.FailedLoginAttempts = 0
	}

	user.FailedLoginAttempts++
	user.LastFailedLoginAt = null.TimeFrom(s.clock.Now())

	if _, err := user.Update(ctx, s.db, boil.Whitelist(
		models.UserColumns.FailedLoginAttempts,
		models.UserColumns.LastFailedLoginAt,
		models.UserColumns.UpdatedAt,
	)); err != nil {
		log.Err(err).Str("userID", user.ID).Msg("Failed to record failed login attempt")
	}
}

// resetFailedLogins clears the counter after a successful authentication.
func (s *Service) resetFailedLogins(ctx context.Context, user *models.User) {
	if user.FailedLoginAttempts == 0 && !user.LastFailedLoginAt.Valid {
		return
	}

	log := util.LogFromContext(ctx)

	user.FailedLoginAttempts = 0
	user.LastFailedLoginAt = null.Time{}

	if _, err := user.Update(ctx, s.db, boil.Whitelist(
		models.UserColumns.FailedLoginAttempts,
		models.UserColumns.LastFailedLoginAt,
		models.UserColumns.UpdatedAt,
	)); err != nil {
		log.Err(err).Str("userID", user.ID).Msg("Failed to reset failed login attempts")
	}
}
