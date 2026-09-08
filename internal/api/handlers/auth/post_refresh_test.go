package auth_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/aarondl/sqlboiler/v4/boil"

	"github.com/idowuseyi/cloudtecko-backend-go/internal/api"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/api/httperrors"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/auth"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/models"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/test"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/test/fixtures"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/types"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util/hashing"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPostRefreshSuccess(t *testing.T) {
	test.WithTestServer(t, func(s *api.Server) {
		ctx := t.Context()
		fix := fixtures.Fixtures()
		payload := test.GenericPayload{
			"refresh_token": fixtures.User1RefreshToken1Plain,
		}

		res := test.PerformRequest(t, s, "POST", "/api/v1/auth/refresh", payload, nil)
		assert.Equal(t, http.StatusOK, res.Result().StatusCode)

		var response types.PostLoginResponse
		test.ParseResponseAndValidate(t, res, &response)

		assert.NotEmpty(t, response.AccessToken)
		assert.NotEqual(t, fixtures.User1AccessToken1Plain, response.AccessToken)
		assert.NotEmpty(t, response.RefreshToken)
		assert.NotEqual(t, fixtures.User1RefreshToken1Plain, response.RefreshToken)
		assert.Equal(t, int64(s.Config.Auth.AccessTokenValidity.Seconds()), *response.ExpiresIn)
		assert.Equal(t, auth.TokenTypeBearer, *response.TokenType)

		// The consumed token is RETAINED rather than deleted. A deleted row is
		// indistinguishable from a token that never existed, which is exactly what
		// made replay undetectable; keeping it is what allows reuse detection.
		err := fix.User1RefreshToken1.Reload(ctx, s.DB)
		require.NoError(t, err)
		assert.True(t, fix.User1RefreshToken1.ConsumedAt.Valid, "the rotated token must be marked consumed")

		// The replacement stays in the same rotation family, so a later replay can
		// revoke the whole chain.
		newToken, err := models.RefreshTokens(
			models.RefreshTokenWhere.TokenHash.EQ(hashing.HashToken(response.RefreshToken.String())),
		).One(ctx, s.DB)
		require.NoError(t, err)
		assert.Equal(t, fix.User1RefreshToken1.FamilyID, newToken.FamilyID)
		assert.False(t, newToken.ConsumedAt.Valid)
	})
}

// Presenting an already-rotated refresh token means the chain leaked. We cannot
// tell a thief from a racing client, so every session derived from the family
// must end.
func TestPostRefreshReuseRevokesFamily(t *testing.T) {
	test.WithTestServer(t, func(s *api.Server) {
		ctx := t.Context()

		payload := test.GenericPayload{"refresh_token": fixtures.User1RefreshToken1Plain}

		res := test.PerformRequest(t, s, "POST", "/api/v1/auth/refresh", payload, nil)
		require.Equal(t, http.StatusOK, res.Result().StatusCode)

		var rotated types.PostLoginResponse
		test.ParseResponseAndValidate(t, res, &rotated)

		// Replay the original, already-consumed token.
		resReuse := test.PerformRequest(t, s, "POST", "/api/v1/auth/refresh", payload, nil)
		test.RequireHTTPError(t, resReuse, httperrors.NewFromEcho(echo.ErrUnauthorized))

		// The token issued by the legitimate rotation is revoked too -- the whole
		// family goes, not just the replayed link.
		resRotated := test.PerformRequest(t, s, "POST", "/api/v1/auth/refresh",
			test.GenericPayload{"refresh_token": rotated.RefreshToken.String()}, nil)
		test.RequireHTTPError(t, resRotated, httperrors.NewFromEcho(echo.ErrUnauthorized))

		// ...as are the user's access tokens, so an already-issued session cannot
		// outlive the revocation.
		count, err := models.AccessTokens(
			models.AccessTokenWhere.UserID.EQ(fixtures.Fixtures().User1.ID),
		).Count(ctx, s.DB)
		require.NoError(t, err)
		assert.Zero(t, count, "access tokens must be revoked when a refresh family is compromised")
	})
}

// A refresh token past its validity is rejected. Before this it had no expiry
// column at all, so a stolen one was valid forever.
func TestPostRefreshExpiredToken(t *testing.T) {
	test.WithTestServer(t, func(s *api.Server) {
		ctx := t.Context()
		fix := fixtures.Fixtures()

		expired, err := models.RefreshTokens(
			models.RefreshTokenWhere.TokenHash.EQ(hashing.HashToken(fixtures.User1RefreshToken1Plain)),
		).One(ctx, s.DB)
		require.NoError(t, err)

		expired.ValidUntil = time.Now().Add(-time.Hour)
		_, err = expired.Update(ctx, s.DB, boil.Whitelist(models.RefreshTokenColumns.ValidUntil, models.RefreshTokenColumns.UpdatedAt))
		require.NoError(t, err)

		res := test.PerformRequest(t, s, "POST", "/api/v1/auth/refresh",
			test.GenericPayload{"refresh_token": fixtures.User1RefreshToken1Plain}, nil)
		test.RequireHTTPError(t, res, httperrors.NewFromEcho(echo.ErrUnauthorized))

		_ = fix
	})
}

func TestPostRefreshUnknownToken(t *testing.T) {
	test.WithTestServer(t, func(s *api.Server) {
		payload := test.GenericPayload{
			"refresh_token": "c094e933-e5f0-4ece-9c10-914f3122cdb6",
		}

		res := test.PerformRequest(t, s, "POST", "/api/v1/auth/refresh", payload, nil)
		test.RequireHTTPError(t, res, httperrors.NewFromEcho(echo.ErrUnauthorized))
	})
}

func TestPostRefreshDeactivatedUser(t *testing.T) {
	test.WithTestServer(t, func(s *api.Server) {
		ctx := t.Context()
		fix := fixtures.Fixtures()
		payload := test.GenericPayload{
			"refresh_token": fixtures.UserDeactivatedRefreshToken1Plain,
		}

		res := test.PerformRequest(t, s, "POST", "/api/v1/auth/refresh", payload, nil)
		test.RequireHTTPError(t, res, httperrors.ErrForbiddenUserDeactivated)

		err := fix.UserDeactivatedRefreshToken1.Reload(ctx, s.DB)
		require.NoError(t, err)
	})
}

func TestPostRefreshBadRequest(t *testing.T) {
	test.WithTestServer(t, func(s *api.Server) {
		tests := []struct {
			name    string
			payload test.GenericPayload
		}{
			{
				name:    "MissingRefreshToken",
				payload: test.GenericPayload{},
			},
			{
				name: "EmptyRefreshToken",
				payload: test.GenericPayload{
					"refresh_token": "",
				},
			},
			{
				name: "InvalidToken",
				payload: test.GenericPayload{
					"refresh_token": "not a valid token",
				},
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				res := test.PerformRequest(t, s, "POST", "/api/v1/auth/refresh", tt.payload, nil)
				assert.Equal(t, http.StatusBadRequest, res.Result().StatusCode)

				var response httperrors.HTTPValidationError
				test.ParseResponseAndValidate(t, res, &response)

				test.Snapshoter.Save(t, response)
			})
		}
	})
}
