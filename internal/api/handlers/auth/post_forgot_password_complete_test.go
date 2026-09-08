package auth_test

import (
	"database/sql"
	"net/http"
	"testing"
	"time"

	"github.com/aarondl/null/v8"
	"github.com/aarondl/sqlboiler/v4/boil"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/api"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/api/httperrors"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/models"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/test"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/test/fixtures"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/types"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util/hashing"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPostForgotPasswordCompleteSuccess(t *testing.T) {
	test.WithTestServer(t, func(s *api.Server) {
		ctx := t.Context()
		fix := fixtures.Fixtures()

		resetTokenPlain, resetTokenHash, err := hashing.NewToken()
		require.NoError(t, err)

		passwordResetToken := models.PasswordResetToken{
			TokenHash:  resetTokenHash,
			UserID:     fix.User1.ID,
			ValidUntil: s.Clock.Now().Add(s.Config.Auth.PasswordResetTokenValidity),
		}

		err = passwordResetToken.Insert(ctx, s.DB, boil.Infer())
		require.NoError(t, err)

		payload := test.GenericPayload{
			"token":    resetTokenPlain,
			"password": newPassword,
		}

		res := test.PerformRequest(t, s, "POST", "/api/v1/auth/forgot-password/complete", payload, nil)
		assert.Equal(t, http.StatusOK, res.Result().StatusCode)

		var response types.PostLoginResponse
		test.ParseResponseAndValidate(t, res, &response)

		assert.NotEmpty(t, response.AccessToken)
		assert.NotEqual(t, fixtures.User1AccessToken1Plain, response.AccessToken)
		assert.NotEmpty(t, response.RefreshToken)
		assert.NotEqual(t, fixtures.User1RefreshToken1Plain, *response.RefreshToken)
		test.Snapshoter.Skip([]string{"AccessToken", "RefreshToken"}).Save(t, response)

		err = fix.User1AccessToken1.Reload(ctx, s.DB)
		require.ErrorIs(t, err, sql.ErrNoRows)
		err = fix.User1RefreshToken1.Reload(ctx, s.DB)
		require.ErrorIs(t, err, sql.ErrNoRows)

		cnt, err := fix.User1.AccessTokens().Count(ctx, s.DB)
		require.NoError(t, err)
		assert.Equal(t, int64(1), cnt)

		cnt, err = fix.User1.RefreshTokens().Count(ctx, s.DB)
		require.NoError(t, err)
		assert.Equal(t, int64(1), cnt)

		err = fix.User1.Reload(ctx, s.DB)
		require.NoError(t, err)
		assert.NotEqual(t, fixtures.HashedTestUserPassword, fix.User1.Password.String)
	})
}

func TestPostForgotPasswordCompleteUnknownToken(t *testing.T) {
	test.WithTestServer(t, func(s *api.Server) {
		ctx := t.Context()
		fix := fixtures.Fixtures()

		payload := test.GenericPayload{
			"token":    "fd5c04ea-f39c-49e9-bb40-7f570ed1f66f",
			"password": newPassword,
		}

		res := test.PerformRequest(t, s, "POST", "/api/v1/auth/forgot-password/complete", payload, nil)
		test.RequireHTTPError(t, res, httperrors.ErrNotFoundTokenNotFound)

		err := fix.User1AccessToken1.Reload(ctx, s.DB)
		require.NoError(t, err)
		err = fix.User1RefreshToken1.Reload(ctx, s.DB)
		require.NoError(t, err)

		cnt, err := fix.User1.AccessTokens().Count(ctx, s.DB)
		require.NoError(t, err)
		assert.Equal(t, int64(1), cnt)

		cnt, err = fix.User1.RefreshTokens().Count(ctx, s.DB)
		require.NoError(t, err)
		assert.Equal(t, int64(1), cnt)

		err = fix.User1.Reload(ctx, s.DB)
		require.NoError(t, err)
		assert.Equal(t, fixtures.HashedTestUserPassword, fix.User1.Password.String)
	})
}

func TestPostForgotPasswordCompleteExpiredToken(t *testing.T) {
	test.WithTestServer(t, func(s *api.Server) {
		ctx := t.Context()
		fix := fixtures.Fixtures()

		resetTokenPlain, resetTokenHash, err := hashing.NewToken()
		require.NoError(t, err)

		passwordResetToken := models.PasswordResetToken{
			TokenHash:  resetTokenHash,
			UserID:     fix.User1.ID,
			ValidUntil: s.Clock.Now().Add(time.Minute * -10),
		}

		err = passwordResetToken.Insert(ctx, s.DB, boil.Infer())
		require.NoError(t, err)

		payload := test.GenericPayload{
			"token":    resetTokenPlain,
			"password": newPassword,
		}

		res := test.PerformRequest(t, s, "POST", "/api/v1/auth/forgot-password/complete", payload, nil)
		test.RequireHTTPError(t, res, httperrors.ErrConflictTokenExpired)

		err = fix.User1AccessToken1.Reload(ctx, s.DB)
		require.NoError(t, err)
		err = fix.User1RefreshToken1.Reload(ctx, s.DB)
		require.NoError(t, err)

		cnt, err := fix.User1.AccessTokens().Count(ctx, s.DB)
		require.NoError(t, err)
		assert.Equal(t, int64(1), cnt)

		cnt, err = fix.User1.RefreshTokens().Count(ctx, s.DB)
		require.NoError(t, err)
		assert.Equal(t, int64(1), cnt)

		err = fix.User1.Reload(ctx, s.DB)
		require.NoError(t, err)
		assert.Equal(t, fixtures.HashedTestUserPassword, fix.User1.Password.String)
	})
}

func TestPostForgotPasswordCompleteDeactivatedUser(t *testing.T) {
	test.WithTestServer(t, func(s *api.Server) {
		ctx := t.Context()
		fix := fixtures.Fixtures()

		resetTokenPlain, resetTokenHash, err := hashing.NewToken()
		require.NoError(t, err)

		passwordResetToken := models.PasswordResetToken{
			TokenHash:  resetTokenHash,
			UserID:     fix.UserDeactivated.ID,
			ValidUntil: s.Clock.Now().Add(s.Config.Auth.PasswordResetTokenValidity),
		}

		err = passwordResetToken.Insert(ctx, s.DB, boil.Infer())
		require.NoError(t, err)

		payload := test.GenericPayload{
			"token":    resetTokenPlain,
			"password": newPassword,
		}

		res := test.PerformRequest(t, s, "POST", "/api/v1/auth/forgot-password/complete", payload, nil)
		test.RequireHTTPError(t, res, httperrors.ErrForbiddenUserDeactivated)

		err = fix.UserDeactivatedAccessToken1.Reload(ctx, s.DB)
		require.NoError(t, err)
		err = fix.UserDeactivatedRefreshToken1.Reload(ctx, s.DB)
		require.NoError(t, err)

		cnt, err := fix.UserDeactivated.AccessTokens().Count(ctx, s.DB)
		require.NoError(t, err)
		assert.Equal(t, int64(1), cnt)

		cnt, err = fix.UserDeactivated.RefreshTokens().Count(ctx, s.DB)
		require.NoError(t, err)
		assert.Equal(t, int64(1), cnt)

		err = fix.UserDeactivated.Reload(ctx, s.DB)
		require.NoError(t, err)
		assert.Equal(t, fixtures.HashedTestUserPassword, fix.UserDeactivated.Password.String)
	})
}

func TestPostForgotPasswordCompleteUserWithoutPassword(t *testing.T) {
	test.WithTestServer(t, func(s *api.Server) {
		ctx := t.Context()
		fix := fixtures.Fixtures()

		resetTokenPlain, resetTokenHash, err := hashing.NewToken()
		require.NoError(t, err)

		passwordResetToken := models.PasswordResetToken{
			TokenHash:  resetTokenHash,
			UserID:     fix.User2.ID,
			ValidUntil: s.Clock.Now().Add(s.Config.Auth.PasswordResetTokenValidity),
		}

		err = passwordResetToken.Insert(ctx, s.DB, boil.Infer())
		require.NoError(t, err)

		payload := test.GenericPayload{
			"token":    resetTokenPlain,
			"password": newPassword,
		}

		fix.User2.Password = null.String{}
		rowsAff, err := fix.User2.Update(t.Context(), s.DB, boil.Infer())
		require.NoError(t, err)
		require.Equal(t, int64(1), rowsAff)

		res := test.PerformRequest(t, s, "POST", "/api/v1/auth/forgot-password/complete", payload, nil)
		test.RequireHTTPError(t, res, httperrors.ErrForbiddenNotLocalUser)

		err = fix.User2AccessToken1.Reload(ctx, s.DB)
		require.NoError(t, err)
		err = fix.User2RefreshToken1.Reload(ctx, s.DB)
		require.NoError(t, err)

		cnt, err := fix.User2.AccessTokens().Count(ctx, s.DB)
		require.NoError(t, err)
		assert.Equal(t, int64(1), cnt)

		cnt, err = fix.User2.RefreshTokens().Count(ctx, s.DB)
		require.NoError(t, err)
		assert.Equal(t, int64(1), cnt)

		err = fix.User2.Reload(ctx, s.DB)
		require.NoError(t, err)
		assert.False(t, fix.User2.Password.Valid)
	})
}

func TestPostForgotPasswordCompleteBadRequest(t *testing.T) {
	tests := []struct {
		name    string
		payload test.GenericPayload
	}{
		{
			name: "MissingToken",
			payload: test.GenericPayload{
				"password": "correct horse battery stable",
			},
		},
		{
			name: "MissingPassword",
			payload: test.GenericPayload{
				"token": "7b6e2366-7806-421f-bd56-ffcb39d7b1ee",
			},
		},
		{
			name: "InvalidToken",
			payload: test.GenericPayload{
				"token":    "definitelydoesnotexist",
				"password": "correct horse battery stable",
			},
		},
		{
			name: "EmptyToken",
			payload: test.GenericPayload{
				"password": "correct horse battery stable",
				"token":    "",
			},
		},
		{
			name: "EmptyPassword",
			payload: test.GenericPayload{
				"token":    "42deb737-fa9c-4e9e-bdce-e33b829c72f7",
				"password": "",
			},
		},
	}

	test.WithTestServer(t, func(s *api.Server) {
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				res := test.PerformRequest(t, s, "POST", "/api/v1/auth/forgot-password/complete", tt.payload, nil)
				assert.Equal(t, http.StatusBadRequest, res.Result().StatusCode)

				var response httperrors.HTTPValidationError
				test.ParseResponseAndValidate(t, res, &response)

				test.Snapshoter.Save(t, response)
			})
		}
	})
}

// A reset link must work exactly once. Before this the token was validated and
// then left in place for the rest of its 15-minute validity, so anyone who later
// obtained it -- a shared mailbox, browser history, a forwarded mail -- could
// reset the password again.
func TestPostForgotPasswordCompleteTokenIsSingleUse(t *testing.T) {
	test.WithTestServer(t, func(s *api.Server) {
		ctx := t.Context()
		fix := fixtures.Fixtures()

		resetTokenPlain, resetTokenHash, err := hashing.NewToken()
		require.NoError(t, err)

		passwordResetToken := models.PasswordResetToken{
			TokenHash:  resetTokenHash,
			UserID:     fix.User1.ID,
			ValidUntil: s.Clock.Now().Add(s.Config.Auth.PasswordResetTokenValidity),
		}
		require.NoError(t, passwordResetToken.Insert(ctx, s.DB, boil.Infer()))

		payload := test.GenericPayload{"token": resetTokenPlain, "password": "chair lamp rocket vinegar"}

		res := test.PerformRequest(t, s, "POST", "/api/v1/auth/forgot-password/complete", payload, nil)
		require.Equal(t, http.StatusOK, res.Result().StatusCode)

		// The row is gone...
		count, err := models.PasswordResetTokens(
			models.PasswordResetTokenWhere.TokenHash.EQ(resetTokenHash),
		).Count(ctx, s.DB)
		require.NoError(t, err)
		assert.Zero(t, count, "a used reset token must be deleted")

		// ...and replaying the link fails.
		resReplay := test.PerformRequest(t, s, "POST", "/api/v1/auth/forgot-password/complete",
			test.GenericPayload{"token": resetTokenPlain, "password": "harbour ladder mango quilt"}, nil)
		assert.Equal(t, http.StatusNotFound, resReplay.Result().StatusCode, "a reset link must not be replayable")
	})
}

// The blocklist is what stops a password that satisfies the length rule but is
// still trivially guessable.
func TestPostForgotPasswordCompleteRejectsWeakPasswords(t *testing.T) {
	test.WithTestServer(t, func(s *api.Server) {
		ctx := t.Context()
		fix := fixtures.Fixtures()

		for _, weak := range []string{
			"aaaaaaaaaaaaaaaa",  // long enough, no entropy
			"123456789012345",   // sequential
			"passwordpassword",  // common
			"user1-my-password", // contains the username
		} {
			resetTokenPlain, resetTokenHash, err := hashing.NewToken()
			require.NoError(t, err)

			token := models.PasswordResetToken{
				TokenHash:  resetTokenHash,
				UserID:     fix.User1.ID,
				ValidUntil: s.Clock.Now().Add(s.Config.Auth.PasswordResetTokenValidity),
			}
			require.NoError(t, token.Insert(ctx, s.DB, boil.Infer()))

			res := test.PerformRequest(t, s, "POST", "/api/v1/auth/forgot-password/complete",
				test.GenericPayload{"token": resetTokenPlain, "password": weak}, nil)
			assert.Equal(t, http.StatusBadRequest, res.Result().StatusCode, "%q must be rejected", weak)
		}
	})
}
