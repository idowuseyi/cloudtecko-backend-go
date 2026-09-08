package auth_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/aarondl/null/v8"
	"github.com/aarondl/sqlboiler/v4/boil"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/api"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/api/httperrors"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/auth"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/config"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/models"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/test"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/test/fixtures"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/types"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPostLoginSuccess(t *testing.T) {
	test.WithTestServer(t, func(s *api.Server) {
		fix := fixtures.Fixtures()
		payload := test.GenericPayload{
			"username": fix.User1.Username,
			"password": fixtures.PlainTestUserPassword,
		}

		res := test.PerformRequest(t, s, "POST", "/api/v1/auth/login", payload, nil)
		assert.Equal(t, http.StatusOK, res.Result().StatusCode)

		var response types.PostLoginResponse
		test.ParseResponseAndValidate(t, res, &response)

		assert.NotEmpty(t, response.AccessToken)
		assert.NotEqual(t, fixtures.User1AccessToken1Plain, response.AccessToken)
		assert.NotEmpty(t, response.RefreshToken)
		assert.NotEqual(t, fixtures.User1RefreshToken1Plain, response.RefreshToken)
		assert.Equal(t, int64(s.Config.Auth.AccessTokenValidity.Seconds()), *response.ExpiresIn)
		assert.Equal(t, auth.TokenTypeBearer, *response.TokenType)
	})
}

func TestPostLoginSuccessLowercaseTrimWhitespaces(t *testing.T) {
	test.WithTestServer(t, func(s *api.Server) {
		fix := fixtures.Fixtures()
		payload := test.GenericPayload{
			"username": fmt.Sprintf(" %s ", strings.ToUpper(fix.User1.Username.String)),
			"password": fixtures.PlainTestUserPassword,
		}

		res := test.PerformRequest(t, s, "POST", "/api/v1/auth/login", payload, nil)
		assert.Equal(t, http.StatusOK, res.Result().StatusCode)

		var response types.PostLoginResponse
		test.ParseResponseAndValidate(t, res, &response)

		assert.NotEmpty(t, response.AccessToken)
		assert.NotEqual(t, fixtures.User1AccessToken1Plain, response.AccessToken)
		assert.NotEmpty(t, response.RefreshToken)
		assert.NotEqual(t, fixtures.User1RefreshToken1Plain, response.RefreshToken)
		assert.Equal(t, int64(s.Config.Auth.AccessTokenValidity.Seconds()), *response.ExpiresIn)
		assert.Equal(t, auth.TokenTypeBearer, *response.TokenType)
	})
}

func TestPostLoginInvalidCredentials(t *testing.T) {
	test.WithTestServer(t, func(s *api.Server) {
		fix := fixtures.Fixtures()
		payload := test.GenericPayload{
			"username": fix.User1.Username,
			"password": "not my password",
		}

		res := test.PerformRequest(t, s, "POST", "/api/v1/auth/login", payload, nil)
		test.RequireHTTPError(t, res, httperrors.NewFromEcho(echo.ErrUnauthorized))
	})
}

func TestPostLoginUnknownUser(t *testing.T) {
	test.WithTestServer(t, func(s *api.Server) {
		payload := test.GenericPayload{
			"username": "definitelydoesnotexist@example.com",
			"password": fixtures.PlainTestUserPassword,
		}

		res := test.PerformRequest(t, s, "POST", "/api/v1/auth/login", payload, nil)
		test.RequireHTTPError(t, res, httperrors.NewFromEcho(echo.ErrUnauthorized))
	})
}

func TestPostLoginDeactivatedUser(t *testing.T) {
	test.WithTestServer(t, func(s *api.Server) {
		fix := fixtures.Fixtures()
		payload := test.GenericPayload{
			"username": fix.UserDeactivated.Username,
			"password": fixtures.PlainTestUserPassword,
		}

		res := test.PerformRequest(t, s, "POST", "/api/v1/auth/login", payload, nil)
		test.RequireHTTPError(t, res, httperrors.ErrForbiddenUserDeactivated)
	})
}

func TestPostLoginUserWithoutPassword(t *testing.T) {
	test.WithTestServer(t, func(s *api.Server) {
		fix := fixtures.Fixtures()
		payload := test.GenericPayload{
			"username": fix.User2.Username,
			"password": fixtures.PlainTestUserPassword,
		}

		fix.User2.Password = null.String{}
		rowsAff, err := fix.User2.Update(t.Context(), s.DB, boil.Infer())
		require.NoError(t, err)
		require.Equal(t, int64(1), rowsAff)

		res := test.PerformRequest(t, s, "POST", "/api/v1/auth/login", payload, nil)
		test.RequireHTTPError(t, res, httperrors.NewFromEcho(echo.ErrUnauthorized))
	})
}

func TestPostLoginBadRequest(t *testing.T) {
	test.WithTestServer(t, func(s *api.Server) {
		fix := fixtures.Fixtures()

		tests := []struct {
			name    string
			payload test.GenericPayload
		}{
			{
				name: "InvalidUsername",
				payload: test.GenericPayload{
					"username": "definitely not an email",
					"password": fixtures.PlainTestUserPassword,
				},
			},
			{
				name: "MissingUsername",
				payload: test.GenericPayload{
					"password": fixtures.PlainTestUserPassword,
				},
			},
			{
				name: "MissingPassword",
				payload: test.GenericPayload{
					"username": fix.User1.Username,
				},
			},
			{
				name: "EmptyUsername",
				payload: test.GenericPayload{
					"username": "",
					"password": fixtures.PlainTestUserPassword,
				},
			},
			{
				name: "EmptyPassword",
				payload: test.GenericPayload{
					"username": fix.User1.Username,
					"password": "",
				},
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				res := test.PerformRequest(t, s, "POST", "/api/v1/auth/login", tt.payload, nil)
				assert.Equal(t, http.StatusBadRequest, res.Result().StatusCode)

				var response httperrors.HTTPValidationError
				test.ParseResponseAndValidate(t, res, &response)

				test.Snapshoter.Save(t, response)
			})
		}
	})
}

// The password minimum applies only where a password is SET, never where one is
// verified. If login enforced it, any user whose password predates the policy
// would be rejected at validation before the credential check -- locked out
// rather than told to reset. A short password must therefore fail
// authentication (401), not validation (400).
func TestPostLoginDoesNotApplyPasswordMinimumLength(t *testing.T) {
	test.WithTestServer(t, func(s *api.Server) {
		fix := fixtures.Fixtures()

		res := test.PerformRequest(t, s, "POST", "/api/v1/auth/login", test.GenericPayload{
			"username": fix.User1.Username,
			"password": "x",
		}, nil)

		require.Equal(t, http.StatusUnauthorized, res.Result().StatusCode,
			"a short password must be rejected as wrong, not as invalid input")
	})
}

// NIST requires limiting CONSECUTIVE failed attempts per account. The IP rate
// limiter does not cover this: an attacker spreading attempts across addresses
// never trips it while hammering one account.
func TestPostLoginThrottlesAfterRepeatedFailures(t *testing.T) {
	config := config.DefaultServiceConfigFromEnv()
	config.Auth.LoginMaxFailedAttempts = 3
	config.Auth.LoginFailureWindow = 15 * time.Minute
	// The per-IP limiter would answer 429 long before the account throttle
	// engaged, and this test is about the account throttle.
	config.Echo.EnableRateLimitMiddleware = false

	test.WithTestServerConfigurable(t, config, func(s *api.Server) {
		ctx := t.Context()
		fix := fixtures.Fixtures()

		wrong := test.GenericPayload{"username": fix.User1.Username, "password": "definitely-the-wrong-password"}
		right := test.GenericPayload{"username": fix.User1.Username, "password": fixtures.PlainTestUserPassword}

		for i := range 3 {
			res := test.PerformRequest(t, s, "POST", "/api/v1/auth/login", wrong, nil)
			require.Equal(t, http.StatusUnauthorized, res.Result().StatusCode, "attempt %d", i+1)
		}

		require.NoError(t, fix.User1.Reload(ctx, s.DB))
		assert.Equal(t, 3, fix.User1.FailedLoginAttempts)

		// Now throttled: even the CORRECT password is refused, and with the same
		// 401 as a wrong one so the throttle is not an enumeration oracle.
		res := test.PerformRequest(t, s, "POST", "/api/v1/auth/login", right, nil)
		require.Equal(t, http.StatusUnauthorized, res.Result().StatusCode,
			"the correct password must be refused while throttled")
	})
}

// The throttle is time-bounded on purpose -- a permanent lockout would let
// anyone who knows a username deny that user access indefinitely.
func TestPostLoginThrottleExpiresAndResetsOnSuccess(t *testing.T) {
	config := config.DefaultServiceConfigFromEnv()
	config.Auth.LoginMaxFailedAttempts = 3
	config.Auth.LoginFailureWindow = 15 * time.Minute
	config.Echo.EnableRateLimitMiddleware = false

	test.WithTestServerConfigurable(t, config, func(s *api.Server) {
		ctx := t.Context()
		fix := fixtures.Fixtures()

		wrong := test.GenericPayload{"username": fix.User1.Username, "password": "definitely-the-wrong-password"}

		for range 3 {
			test.PerformRequest(t, s, "POST", "/api/v1/auth/login", wrong, nil)
		}

		// Age the last failure past the window.
		require.NoError(t, fix.User1.Reload(ctx, s.DB))
		fix.User1.LastFailedLoginAt = null.TimeFrom(s.Clock.Now().Add(-16 * time.Minute))
		_, err := fix.User1.Update(ctx, s.DB, boil.Whitelist(models.UserColumns.LastFailedLoginAt))
		require.NoError(t, err)

		res := test.PerformRequest(t, s, "POST", "/api/v1/auth/login",
			test.GenericPayload{"username": fix.User1.Username, "password": fixtures.PlainTestUserPassword}, nil)
		require.Equal(t, http.StatusOK, res.Result().StatusCode, "the throttle must expire")

		// A successful login clears the counter, so unrelated future mistakes start
		// from zero.
		require.NoError(t, fix.User1.Reload(ctx, s.DB))
		assert.Zero(t, fix.User1.FailedLoginAttempts)
		assert.False(t, fix.User1.LastFailedLoginAt.Valid)
	})
}
