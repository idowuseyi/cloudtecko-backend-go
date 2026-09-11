package auth_test

import (
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/aarondl/null/v8"
	"github.com/aarondl/sqlboiler/v4/queries/qm"
	"github.com/go-openapi/swag/conv"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/api"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/api/httperrors"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/auth"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/config"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/models"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/test"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/test/fixtures"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/types"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util/db"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util/hashing"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util/url"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPostRegisterSuccess(t *testing.T) {
	test.WithTestServer(t, func(s *api.Server) {
		ctx := t.Context()

		now := time.Date(2025, 2, 5, 11, 42, 30, 0, time.UTC)
		test.SetMockClock(t, s, now)

		username := "usernew@example.com"
		payload := test.GenericPayload{
			"username": username,
			"password": fixtures.PlainTestUserPassword,
		}

		res := test.PerformRequest(t, s, "POST", "/api/v1/auth/register", payload, nil)

		require.Equal(t, http.StatusOK, res.Result().StatusCode)

		var response types.PostLoginResponse
		test.ParseResponseAndValidate(t, res, &response)

		assert.NotEmpty(t, response.AccessToken)
		assert.NotEmpty(t, response.RefreshToken)
		assert.Equal(t, int64(s.Config.Auth.AccessTokenValidity.Seconds()), *response.ExpiresIn)
		assert.Equal(t, auth.TokenTypeBearer, *response.TokenType)

		user, err := models.Users(
			models.UserWhere.Username.EQ(null.StringFrom(username)),
			qm.Load(models.UserRels.AppUserProfile),
			qm.Load(models.UserRels.AccessTokens),
			qm.Load(models.UserRels.RefreshTokens),
		).One(ctx, s.DB)
		require.NoError(t, err)
		assert.Equal(t, null.StringFrom(username), user.Username)
		assert.True(t, user.LastAuthenticatedAt.Valid)
		assert.Equal(t, now, user.LastAuthenticatedAt.Time)
		assert.EqualValues(t, s.Config.Auth.DefaultUserScopes, user.Scopes)

		assert.NotNil(t, user.R.AppUserProfile)
		assert.False(t, user.R.AppUserProfile.LegalAcceptedAt.Valid)

		assert.Len(t, user.R.AccessTokens, 1)
		// The row stores only the hash, so the check is that the token handed to
		// the client hashes to what was persisted -- never that they are equal.
		assert.Equal(t, user.R.AccessTokens[0].TokenHash, hashing.HashToken(response.AccessToken.String()))
		assert.NotEqual(t, response.AccessToken.String(), string(user.R.AccessTokens[0].TokenHash), "the plaintext token must not be stored")
		assert.Len(t, user.R.RefreshTokens, 1)
		assert.Equal(t, user.R.RefreshTokens[0].TokenHash, hashing.HashToken(response.RefreshToken.String()))

		res2 := test.PerformRequest(t, s, "POST", "/api/v1/auth/login", payload, nil)
		assert.Equal(t, http.StatusOK, res2.Result().StatusCode)

		var response2 types.PostLoginResponse
		test.ParseResponseAndValidate(t, res2, &response2)

		assert.NotEmpty(t, response2.AccessToken)
		assert.NotEqual(t, response.AccessToken, *response2.AccessToken)
		assert.NotEmpty(t, response2.RefreshToken)
		assert.NotEqual(t, response.RefreshToken, *response2.RefreshToken)
		assert.Equal(t, int64(s.Config.Auth.AccessTokenValidity.Seconds()), *response2.ExpiresIn)
		assert.Equal(t, auth.TokenTypeBearer, *response2.TokenType)
	})
}

func TestPostRegisterWithConfirmationSuccess(t *testing.T) {
	config := config.DefaultServiceConfigFromEnv()
	config.Auth.RegistrationRequiresConfirmation = true

	test.WithTestServerConfigurable(t, config, func(s *api.Server) {
		ctx := t.Context()

		username := "usernew-with-confirmation@example.com"
		payload := test.GenericPayload{
			"username": username,
			"password": fixtures.PlainTestUserPassword,
		}

		res := test.PerformRequest(t, s, "POST", "/api/v1/auth/register", payload, nil)

		require.Equal(t, http.StatusAccepted, res.Result().StatusCode)

		var response types.RegisterResponse
		test.ParseResponseAndValidate(t, res, &response)

		assert.True(t, conv.Value(response.RequiresConfirmation))

		// expect the user to be normally created
		user, err := models.Users(
			models.UserWhere.Username.EQ(null.StringFrom(username)),
			qm.Load(models.UserRels.AppUserProfile),
			qm.Load(models.UserRels.AccessTokens),
			qm.Load(models.UserRels.RefreshTokens),
			qm.Load(models.UserRels.ConfirmationTokens),
		).One(ctx, s.DB)
		require.NoError(t, err)
		assert.Equal(t, null.StringFrom(username), user.Username)
		assert.True(t, user.LastAuthenticatedAt.Valid)
		assert.EqualValues(t, s.Config.Auth.DefaultUserScopes, user.Scopes)
		assert.False(t, user.IsActive)
		assert.True(t, user.RequiresConfirmation)

		assert.NotNil(t, user.R.AppUserProfile)
		assert.False(t, user.R.AppUserProfile.LegalAcceptedAt.Valid)

		// expect the user to have no access or refresh tokens
		assert.Empty(t, user.R.AccessTokens)
		assert.Empty(t, user.R.RefreshTokens)
		require.Len(t, user.R.ConfirmationTokens, 1)
		confirmationToken := user.R.ConfirmationTokens[0]

		// expect the login to fail
		res2 := test.PerformRequest(t, s, "POST", "/api/v1/auth/login", payload, nil)
		test.RequireHTTPError(t, res2, httperrors.ErrForbiddenUserDeactivated)

		// expect the confirmation email to be sent
		mails := test.GetSentMails(t, s.Mailer)
		require.Len(t, mails, 1)

		mail := mails[0]
		assert.Equal(t, username, mail.To[0])

		// The token cannot be read back out of the database, so take the same path
		// the user does -- the link in the mail -- and check it hashes to the
		// stored row.
		matches := regexp.MustCompile(`token=([0-9a-f-]{36})`).FindStringSubmatch(string(mail.HTML))
		require.Len(t, matches, 2, "no confirmation token found in mail")
		assert.Equal(t, hashing.HashToken(matches[1]), confirmationToken.TokenHash)

		expectedConfirmationLink, err := url.ConfirmationDeeplinkURL(s.Config, matches[1])
		require.NoError(t, err)
		assert.Contains(t, string(mail.HTML), expectedConfirmationLink.String())

		// directly register again should trigger the debounce
		// and not create a new confirmation token
		registerAgainRes := test.PerformRequest(t, s, "POST", "/api/v1/auth/register", payload, nil)
		require.Equal(t, http.StatusAccepted, registerAgainRes.Result().StatusCode)

		var registerAgainResponse types.RegisterResponse
		test.ParseResponseAndValidate(t, registerAgainRes, &registerAgainResponse)

		// expect the confirmation to be required
		assert.True(t, conv.Value(registerAgainResponse.RequiresConfirmation))

		confirmationTokenCount, err := user.ConfirmationTokens().Count(ctx, s.DB)
		require.NoError(t, err)

		assert.EqualValues(t, 1, confirmationTokenCount)

		// register later again
		test.SetMockClock(t, s, s.Clock.Now().Add(config.Auth.ConfirmationTokenDebounceDuration+time.Second))

		registerLaterAgainRes := test.PerformRequest(t, s, "POST", "/api/v1/auth/register", payload, nil)
		require.Equal(t, http.StatusAccepted, registerLaterAgainRes.Result().StatusCode)

		var registerLaterAgainResponse types.RegisterResponse
		test.ParseResponseAndValidate(t, registerLaterAgainRes, &registerLaterAgainResponse)
		assert.True(t, conv.Value(registerLaterAgainResponse.RequiresConfirmation))

		confirmationTokens, err := models.ConfirmationTokens(
			models.ConfirmationTokenWhere.UserID.EQ(user.ID),
			db.OrderBy(types.OrderDirDesc, models.ConfirmationTokenColumns.CreatedAt),
		).All(ctx, s.DB)
		require.NoError(t, err)

		require.Len(t, confirmationTokens, 2)

		lastSentMail := test.GetLastSentMail(t, s.Mailer)
		require.NotNil(t, lastSentMail)

		assert.Equal(t, username, lastSentMail.To[0])

		resendMatches := regexp.MustCompile(`token=([0-9a-f-]{36})`).FindStringSubmatch(string(lastSentMail.HTML))
		require.Len(t, resendMatches, 2, "no confirmation token found in resent mail")
		assert.Equal(t, hashing.HashToken(resendMatches[1]), confirmationTokens[0].TokenHash)

		expectedConfirmationLink, err = url.ConfirmationDeeplinkURL(s.Config, resendMatches[1])
		require.NoError(t, err)
		assert.Contains(t, string(lastSentMail.HTML), expectedConfirmationLink.String())
	})
}

func TestPostRegisterSuccessLowercaseTrimWhitespaces(t *testing.T) {
	test.WithTestServer(t, func(s *api.Server) {
		ctx := t.Context()

		username := " USERNEW@example.com "
		usernameLowerTrimmed := "usernew@example.com"
		payload := test.GenericPayload{
			"username": username,
			"password": fixtures.PlainTestUserPassword,
			"name":     "Trim Whitespaces",
		}

		res := test.PerformRequest(t, s, "POST", "/api/v1/auth/register", payload, nil)

		require.Equal(t, http.StatusOK, res.Result().StatusCode)

		var response types.PostLoginResponse
		test.ParseResponseAndValidate(t, res, &response)

		assert.NotEmpty(t, response.AccessToken)
		assert.NotEmpty(t, response.RefreshToken)
		assert.Equal(t, int64(s.Config.Auth.AccessTokenValidity.Seconds()), *response.ExpiresIn)
		assert.Equal(t, auth.TokenTypeBearer, *response.TokenType)

		user, err := models.Users(
			models.UserWhere.Username.EQ(null.StringFrom(usernameLowerTrimmed)),
			qm.Load(models.UserRels.AppUserProfile),
			qm.Load(models.UserRels.AccessTokens),
			qm.Load(models.UserRels.RefreshTokens),
		).One(ctx, s.DB)
		require.NoError(t, err)
		assert.Equal(t, null.StringFrom(usernameLowerTrimmed), user.Username)
		assert.True(t, user.LastAuthenticatedAt.Valid)
		assert.WithinDuration(t, s.Clock.Now(), user.LastAuthenticatedAt.Time, time.Second*10)
		assert.EqualValues(t, s.Config.Auth.DefaultUserScopes, user.Scopes)

		assert.NotNil(t, user.R.AppUserProfile)
		assert.False(t, user.R.AppUserProfile.LegalAcceptedAt.Valid)

		assert.Len(t, user.R.AccessTokens, 1)
		// The row stores only the hash, so the check is that the token handed to
		// the client hashes to what was persisted -- never that they are equal.
		assert.Equal(t, user.R.AccessTokens[0].TokenHash, hashing.HashToken(response.AccessToken.String()))
		assert.NotEqual(t, response.AccessToken.String(), string(user.R.AccessTokens[0].TokenHash), "the plaintext token must not be stored")
		assert.Len(t, user.R.RefreshTokens, 1)
		assert.Equal(t, user.R.RefreshTokens[0].TokenHash, hashing.HashToken(response.RefreshToken.String()))

		res2 := test.PerformRequest(t, s, "POST", "/api/v1/auth/login", payload, nil)
		assert.Equal(t, http.StatusOK, res2.Result().StatusCode)

		var response2 types.PostLoginResponse
		test.ParseResponseAndValidate(t, res2, &response2)

		assert.NotEmpty(t, response2.AccessToken)
		assert.NotEqual(t, response.AccessToken, *response2.AccessToken)
		assert.NotEmpty(t, response2.RefreshToken)
		assert.NotEqual(t, response.RefreshToken, *response2.RefreshToken)
		assert.Equal(t, int64(s.Config.Auth.AccessTokenValidity.Seconds()), *response2.ExpiresIn)
		assert.Equal(t, auth.TokenTypeBearer, *response2.TokenType)
	})
}

func TestPostRegisterAlreadyExists(t *testing.T) {
	// S-020: registering an address that already has an account must not be
	// distinguishable from registering a new one.
	//
	// Run with confirmation REQUIRED, because that is the only configuration in
	// which the property can hold: with confirmation disabled a new registration
	// is answered with a session, which an existing address cannot be given, and
	// that difference is itself the oracle. router.Init warns about exactly this
	// at startup, and TestPostRegisterDisclosesWhenConfirmationDisabled below
	// pins the limitation so it cannot be forgotten.
	config := config.DefaultServiceConfigFromEnv()
	config.Auth.RegistrationRequiresConfirmation = true

	test.WithTestServerConfigurable(t, config, func(s *api.Server) {
		ctx := t.Context()

		fix := fixtures.Fixtures()

		existing := test.PerformRequest(t, s, "POST", "/api/v1/auth/register", test.GenericPayload{
			"username": fix.User1.Username,
			"password": fixtures.PlainTestUserPassword,
		}, nil)
		require.Equal(t, http.StatusAccepted, existing.Result().StatusCode)

		fresh := test.PerformRequest(t, s, "POST", "/api/v1/auth/register", test.GenericPayload{
			"username": "definitely-new-address@example.com",
			"password": fixtures.PlainTestUserPassword,
		}, nil)
		require.Equal(t, http.StatusAccepted, fresh.Result().StatusCode)

		assert.Equal(t, fresh.Body.String(), existing.Body.String(),
			"the response for an existing address must be byte-identical to a new one")

		// The existing account is untouched: no extra profile, no new tokens.
		user, err := models.Users(
			models.UserWhere.Username.EQ(fix.User1.Username),
			qm.Load(models.UserRels.AppUserProfile),
			qm.Load(models.UserRels.AccessTokens),
			qm.Load(models.UserRels.RefreshTokens),
		).One(ctx, s.DB)
		require.NoError(t, err)
		assert.Equal(t, user.ID, fix.User1.ID)

		assert.NotNil(t, user.R.AppUserProfile)
		assert.Len(t, user.R.AccessTokens, 1)
		assert.Len(t, user.R.RefreshTokens, 1)
	})
}

// Pins the known limitation rather than leaving it implicit: with confirmation
// disabled, /register DOES disclose, because a new address gets a session and an
// existing one cannot. If this test ever starts failing, the oracle has been
// closed in that configuration too and the caveat can be deleted -- including
// the startup warning in router.Init.
func TestPostRegisterDisclosesWhenConfirmationDisabled(t *testing.T) {
	config := config.DefaultServiceConfigFromEnv()
	config.Auth.RegistrationRequiresConfirmation = false

	test.WithTestServerConfigurable(t, config, func(s *api.Server) {
		fix := fixtures.Fixtures()

		existing := test.PerformRequest(t, s, "POST", "/api/v1/auth/register", test.GenericPayload{
			"username": fix.User1.Username,
			"password": fixtures.PlainTestUserPassword,
		}, nil)

		fresh := test.PerformRequest(t, s, "POST", "/api/v1/auth/register", test.GenericPayload{
			"username": "another-new-address@example.com",
			"password": fixtures.PlainTestUserPassword,
		}, nil)

		assert.Equal(t, http.StatusAccepted, existing.Result().StatusCode)
		assert.Equal(t, http.StatusOK, fresh.Result().StatusCode,
			"a new registration is answered with a session when confirmation is off")
		assert.NotEqual(t, existing.Result().StatusCode, fresh.Result().StatusCode,
			"this difference IS the residual oracle; set SERVER_AUTH_REGISTRATION_REQUIRES_CONFIRMATION=true to close it")
	})
}

func TestPostRegisterBadRequest(t *testing.T) {
	test.WithTestServer(t, func(s *api.Server) {
		fix := fixtures.Fixtures()

		tests := []struct {
			name    string
			payload test.GenericPayload
		}{
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
				name: "InvalidUsername",
				payload: test.GenericPayload{
					"username": "definitely not an email",
					"password": fixtures.PlainTestUserPassword,
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
				res := test.PerformRequest(t, s, "POST", "/api/v1/auth/register", tt.payload, nil)
				assert.Equal(t, http.StatusBadRequest, res.Result().StatusCode)

				var response httperrors.HTTPValidationError
				test.ParseResponseAndValidate(t, res, &response)

				test.Snapshoter.Save(t, response)
			})
		}
	})
}

// Registration enforces the password minimum. 15 is NIST SP 800-63B Rev 4's
// requirement when a password is the only authentication factor, which it is
// here -- there is no MFA in this service.
func TestPostRegisterPasswordMinimumLength(t *testing.T) {
	test.WithTestServer(t, func(s *api.Server) {
		res := test.PerformRequest(t, s, "POST", "/api/v1/auth/register", test.GenericPayload{
			"username": "tooshort@example.com",
			"password": "chairlamprocke", // 14 characters
		}, nil)
		require.Equal(t, http.StatusBadRequest, res.Result().StatusCode, "14 characters must be rejected")

		// Note this is NOT strings.Repeat("a", 15): length alone is not sufficient,
		// the blocklist rejects a password with no entropy however long it is.
		res = test.PerformRequest(t, s, "POST", "/api/v1/auth/register", test.GenericPayload{
			"username": "longenough@example.com",
			"password": "chairlamprocket", // 15 characters
		}, nil)
		require.Equal(t, http.StatusOK, res.Result().StatusCode, "15 characters must be accepted")

		// Length satisfied, entropy not: the blocklist must still refuse it.
		res = test.PerformRequest(t, s, "POST", "/api/v1/auth/register", test.GenericPayload{
			"username": "norepeats@example.com",
			"password": strings.Repeat("a", 15),
		}, nil)
		require.Equal(t, http.StatusBadRequest, res.Result().StatusCode,
			"a long password with no entropy must be rejected")
	})
}
