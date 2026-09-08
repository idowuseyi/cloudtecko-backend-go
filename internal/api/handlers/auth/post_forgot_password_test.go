package auth_test

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/aarondl/null/v8"
	"github.com/aarondl/sqlboiler/v4/boil"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/api"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/api/httperrors"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/config"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/models"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/test"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/test/fixtures"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util/hashing"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resetTokenFromMail pulls the reset token out of the sent mail.
//
// The test can no longer read the token out of the database -- only its hash is
// stored -- so it takes the same path a real user does: the link in the email.
func resetTokenFromMail(t *testing.T, html string) string {
	t.Helper()

	matches := regexp.MustCompile(`set-new-password\?token=([0-9a-f-]{36})`).FindStringSubmatch(html)
	require.Len(t, matches, 2, "no reset token found in mail")

	return matches[1]
}

func TestPostForgotPasswordSuccess(t *testing.T) {
	config := config.DefaultServiceConfigFromEnv()
	config.Auth.PasswordResetTokenDebounceDuration = 60 * time.Second

	test.WithTestServerConfigurable(t, config, func(s *api.Server) {
		ctx := t.Context()
		fix := fixtures.Fixtures()
		payload := test.GenericPayload{
			"username": fix.User1.Username,
		}

		res := test.PerformRequest(t, s, "POST", "/api/v1/auth/forgot-password", payload, nil)
		require.Equal(t, http.StatusNoContent, res.Result().StatusCode)

		mail := test.GetLastSentMail(t, s.Mailer)
		require.NotNil(t, mail)
		firstToken := resetTokenFromMail(t, string(mail.HTML))

		// The mailed token must hash to the stored row, and must not itself appear
		// in the database.
		stored, err := fix.User1.PasswordResetTokens().One(ctx, s.DB)
		require.NoError(t, err)
		assert.Equal(t, hashing.HashToken(firstToken), stored.TokenHash)

		// retrying should not send a new mail because of the debounce time
		{
			res := test.PerformRequest(t, s, "POST", "/api/v1/auth/forgot-password", payload, nil)
			require.Equal(t, http.StatusNoContent, res.Result().StatusCode)

			sentMails := test.GetSentMails(t, s.Mailer)
			assert.Len(t, sentMails, 1)
		}

		// Past the debounce a new mail goes out with a NEW token, and the previous
		// one is invalidated. The old behaviour re-sent the same token, which is
		// impossible now that only its hash is stored.
		{
			_, err = models.PasswordResetTokens().UpdateAll(ctx, s.DB, models.M{
				models.PasswordResetTokenColumns.CreatedAt: s.Clock.Now().Add(-s.Config.Auth.PasswordResetTokenDebounceDuration),
			})
			require.NoError(t, err)

			res := test.PerformRequest(t, s, "POST", "/api/v1/auth/forgot-password", payload, nil)
			require.Equal(t, http.StatusNoContent, res.Result().StatusCode)

			sentMails := test.GetSentMails(t, s.Mailer)
			require.Len(t, sentMails, 2)

			secondToken := resetTokenFromMail(t, string(sentMails[1].HTML))
			assert.NotEqual(t, firstToken, secondToken, "a fresh token must be issued")

			// Exactly one live reset token per user.
			remaining, err := fix.User1.PasswordResetTokens().All(ctx, s.DB)
			require.NoError(t, err)
			require.Len(t, remaining, 1)
			assert.Equal(t, hashing.HashToken(secondToken), remaining[0].TokenHash)
		}
	})
}

// Requesting a second reset must make the first link stop working. This is the
// deliberate behaviour change that hashing forced: the old token cannot be
// re-sent, so it is revoked rather than left live alongside the new one.
func TestPostForgotPasswordInvalidatesPreviousToken(t *testing.T) {
	config := config.DefaultServiceConfigFromEnv()
	config.Auth.PasswordResetTokenDebounceDuration = 0

	test.WithTestServerConfigurable(t, config, func(s *api.Server) {
		ctx := t.Context()
		fix := fixtures.Fixtures()
		payload := test.GenericPayload{"username": fix.User1.Username}

		require.Equal(t, http.StatusNoContent,
			test.PerformRequest(t, s, "POST", "/api/v1/auth/forgot-password", payload, nil).Result().StatusCode)
		firstToken := resetTokenFromMail(t, string(test.GetLastSentMail(t, s.Mailer).HTML))

		require.Equal(t, http.StatusNoContent,
			test.PerformRequest(t, s, "POST", "/api/v1/auth/forgot-password", payload, nil).Result().StatusCode)
		secondToken := resetTokenFromMail(t, string(test.GetLastSentMail(t, s.Mailer).HTML))
		require.NotEqual(t, firstToken, secondToken)

		count, err := models.PasswordResetTokens().Count(ctx, s.DB)
		require.NoError(t, err)
		assert.Equal(t, int64(1), count, "only the newest reset token may remain")

		// The superseded link is dead...
		resOld := test.PerformRequest(t, s, "POST", "/api/v1/auth/forgot-password/complete",
			test.GenericPayload{"token": firstToken, "password": "a-brand-new-password"}, nil)
		assert.Equal(t, http.StatusNotFound, resOld.Result().StatusCode)

		// ...and the newest one works.
		resNew := test.PerformRequest(t, s, "POST", "/api/v1/auth/forgot-password/complete",
			test.GenericPayload{"token": secondToken, "password": "a-brand-new-password"}, nil)
		assert.Equal(t, http.StatusOK, resNew.Result().StatusCode)
	})
}

func TestPostForgotPasswordSuccessLowercaseTrimWhitespaces(t *testing.T) {
	test.WithTestServer(t, func(s *api.Server) {
		ctx := t.Context()
		fix := fixtures.Fixtures()
		payload := test.GenericPayload{
			"username": fmt.Sprintf(" %s ", strings.ToUpper(fix.User1.Username.String)),
		}

		res := test.PerformRequest(t, s, "POST", "/api/v1/auth/forgot-password", payload, nil)
		assert.Equal(t, http.StatusNoContent, res.Result().StatusCode)

		stored, err := fix.User1.PasswordResetTokens().One(ctx, s.DB)
		require.NoError(t, err)

		mail := test.GetLastSentMail(t, s.Mailer)
		require.NotNil(t, mail)
		assert.Equal(t, hashing.HashToken(resetTokenFromMail(t, string(mail.HTML))), stored.TokenHash)
	})
}

func TestPostForgotPasswordUnknownUser(t *testing.T) {
	test.WithTestServer(t, func(s *api.Server) {
		ctx := t.Context()

		payload := test.GenericPayload{
			"username": "definitelydoesnotexist@example.com",
		}

		res := test.PerformRequest(t, s, "POST", "/api/v1/auth/forgot-password", payload, nil)
		assert.Equal(t, http.StatusNoContent, res.Result().StatusCode)

		cnt, err := models.PasswordResetTokens().Count(ctx, s.DB)
		require.NoError(t, err)
		assert.Equal(t, int64(0), cnt)

		mail := test.GetLastSentMail(t, s.Mailer)
		assert.Nil(t, mail)
	})
}

func TestPostForgotPasswordDeactivatedUser(t *testing.T) {
	test.WithTestServer(t, func(s *api.Server) {
		ctx := t.Context()
		fix := fixtures.Fixtures()

		payload := test.GenericPayload{
			"username": fix.UserDeactivated.Username,
		}

		res := test.PerformRequest(t, s, "POST", "/api/v1/auth/forgot-password", payload, nil)
		assert.Equal(t, http.StatusNoContent, res.Result().StatusCode)

		cnt, err := models.PasswordResetTokens().Count(ctx, s.DB)
		require.NoError(t, err)
		assert.Equal(t, int64(0), cnt)

		mail := test.GetLastSentMail(t, s.Mailer)
		assert.Nil(t, mail)
	})
}

func TestPostForgotPasswordUserWithoutPassword(t *testing.T) {
	test.WithTestServer(t, func(s *api.Server) {
		ctx := t.Context()
		fix := fixtures.Fixtures()

		payload := test.GenericPayload{
			"username": fix.User2.Username,
		}

		fix.User2.Password = null.String{}
		rowsAff, err := fix.User2.Update(t.Context(), s.DB, boil.Infer())
		require.NoError(t, err)
		require.Equal(t, int64(1), rowsAff)

		res := test.PerformRequest(t, s, "POST", "/api/v1/auth/forgot-password", payload, nil)
		assert.Equal(t, http.StatusNoContent, res.Result().StatusCode)

		cnt, err := models.PasswordResetTokens().Count(ctx, s.DB)
		require.NoError(t, err)
		assert.Equal(t, int64(0), cnt)

		mail := test.GetLastSentMail(t, s.Mailer)
		assert.Nil(t, mail)
	})
}

func TestPostForgotPasswordBadRequest(t *testing.T) {
	test.WithTestServer(t, func(s *api.Server) {
		ctx := t.Context()

		tests := []struct {
			name    string
			payload test.GenericPayload
		}{
			{
				name:    "MissingUsername",
				payload: test.GenericPayload{},
			},
			{
				name: "EmptyUsername",
				payload: test.GenericPayload{
					"username": "",
				},
			},
			{
				name: "InvalidUsername",
				payload: test.GenericPayload{
					"username": "definitely not an email",
				},
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				res := test.PerformRequest(t, s, "POST", "/api/v1/auth/forgot-password", tt.payload, nil)
				assert.Equal(t, http.StatusBadRequest, res.Result().StatusCode)

				var response httperrors.HTTPValidationError
				test.ParseResponseAndValidate(t, res, &response)

				test.Snapshoter.Save(t, response)

				cnt, err := models.PasswordResetTokens().Count(ctx, s.DB)
				require.NoError(t, err)
				assert.Equal(t, int64(0), cnt)

				mail := test.GetLastSentMail(t, s.Mailer)
				assert.Nil(t, mail)
			})
		}
	})
}
