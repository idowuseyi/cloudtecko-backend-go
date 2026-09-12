package community_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/aarondl/sqlboiler/v4/queries/qm"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/api"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/data/dto"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/models"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/test"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/test/fixtures"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func contactPayload(email string) test.GenericPayload {
	return test.GenericPayload{
		"name":    "Ada Obi",
		"email":   email,
		"subject": "general",
		"message": "I would like to know more about the community.",
	}
}

func TestPostContactMessageStoresAndAccepts(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		res := test.PerformRequest(t, s, "POST", "/api/v1/contact-messages", contactPayload("new-sender@example.com"), nil)
		require.Equal(t, http.StatusAccepted, res.Result().StatusCode)

		stored, err := models.ContactMessages(
			qm.Where("lower(email) = ?", "new-sender@example.com"),
		).One(t.Context(), s.DB)
		require.NoError(t, err)

		assert.Equal(t, "Ada Obi", stored.Name)
		assert.Equal(t, models.ContactMessageStatusNew, stored.Status)
		// The schema refuses a PII row without an expiry, so this must be set.
		assert.False(t, stored.PurgeAfter.IsZero(), "purge_after must be populated on insert")
	})
}

// The throttle must be silent. Answering "you submitted too recently" would
// confirm that an address has used the form.
func TestPostContactMessageThrottleIsSilent(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		first := test.PerformRequest(t, s, "POST", "/api/v1/contact-messages", contactPayload("repeat@example.com"), nil)
		require.Equal(t, http.StatusAccepted, first.Result().StatusCode)

		second := test.PerformRequest(t, s, "POST", "/api/v1/contact-messages", contactPayload("repeat@example.com"), nil)
		assert.Equal(t, http.StatusAccepted, second.Result().StatusCode,
			"a throttled submission is indistinguishable from an accepted one")

		count, err := models.ContactMessages(
			qm.Where("lower(email) = ?", "repeat@example.com"),
		).Count(t.Context(), s.DB)
		require.NoError(t, err)
		assert.Equal(t, int64(1), count, "the second submission was dropped, not stored")
	})
}

// The IP is kept only as a network prefix: enough to correlate abuse, not a
// full address.
func TestContactMessageStoresOnlyAnIPPrefix(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		res := test.PerformRequest(t, s, "POST", "/api/v1/contact-messages", contactPayload("prefix@example.com"), nil)
		require.Equal(t, http.StatusAccepted, res.Result().StatusCode)

		stored, err := models.ContactMessages(
			qm.Where("lower(email) = ?", "prefix@example.com"),
		).One(t.Context(), s.DB)
		require.NoError(t, err)

		if stored.SourceIPPrefix.Valid {
			assert.Contains(t, stored.SourceIPPrefix.String, "/",
				"the stored value must be a network prefix, not a bare address")
		}
	})
}

func TestGetFAQsReturnsOnlyPublished(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		res := test.PerformRequest(t, s, "GET", "/api/v1/faqs", nil, nil)
		require.Equal(t, http.StatusOK, res.Result().StatusCode)

		var response types.FAQList
		test.ParseResponseAndValidate(t, res, &response)

		require.Len(t, response.Items, 1)
		assert.Equal(t, "How do I join Cloudadore Club?", *response.Items[0].Question)
	})
}

// The whole point of the waitlist endpoint: a new address, a duplicate, and a
// previously rejected address must be indistinguishable from outside.
func TestPostMembershipApplicationIsNonDisclosing(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		cases := []struct {
			name  string
			email string
		}{
			{"brand new address", "brand-new@example.com"},
			{"already has a live application", fixtures.Fixtures().ApplicationPending.Email},
			{"already approved", fixtures.Fixtures().ApplicationApproved.Email},
			{"previously rejected", fixtures.Fixtures().ApplicationRejected.Email},
			{"belongs to an existing user", "user1@example.com"},
		}

		for _, testCase := range cases {
			res := test.PerformRequest(t, s, "POST", "/api/v1/membership/applications", test.GenericPayload{
				"email":    testCase.email,
				"fullName": "Someone",
			}, nil)

			assert.Equal(t, http.StatusAccepted, res.Result().StatusCode, "case %q must be indistinguishable", testCase.name)
			assert.Empty(t, res.Body.String(), "case %q must return no body to differ on", testCase.name)
		}
	})
}

func TestPostMembershipApplicationDoesNotDuplicateLiveApplications(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		email := fixtures.Fixtures().ApplicationPending.Email

		res := test.PerformRequest(t, s, "POST", "/api/v1/membership/applications", test.GenericPayload{
			"email": email, "fullName": "Someone Else",
		}, nil)
		require.Equal(t, http.StatusAccepted, res.Result().StatusCode)

		count, err := models.MembershipApplications(
			qm.Where("lower(email) = ?", email),
		).Count(t.Context(), s.DB)
		require.NoError(t, err)
		assert.Equal(t, int64(1), count, "the live application was not duplicated")
	})
}

// Acceptance creates the account. Before it, no user exists for that address --
// which is exactly what makes an unaccepted invitation harmless.
func TestAcceptInvitationCreatesTheAccount(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		email := fixtures.Fixtures().ApplicationApproved.Email

		exists, err := models.Users(qm.Where("lower(username) = ?", email)).Exists(t.Context(), s.DB)
		require.NoError(t, err)
		require.False(t, exists, "approval must not have created a user")

		res := test.PerformRequest(t, s, "POST",
			"/api/v1/membership/invitations/"+fixtures.MembershipInvitationTokenPlain+"/accept",
			test.GenericPayload{"password": "a-sufficiently-long-passphrase"}, nil)
		require.Equal(t, http.StatusOK, res.Result().StatusCode)

		var session types.PostLoginResponse
		test.ParseResponseAndValidate(t, res, &session)
		assert.NotEmpty(t, session.AccessToken)

		user, err := models.Users(qm.Where("lower(username) = ?", email)).One(t.Context(), s.DB)
		require.NoError(t, err)
		assert.True(t, user.IsActive)
		// A password MUST be set. The whole reason this flow exists rather than
		// reusing CompleteRegister is that the latter would leave it empty, making
		// the account permanently unloggable-into.
		assert.True(t, user.Password.Valid, "the accepted account must have a password")
		assert.NotEmpty(t, user.Password.String)
	})
}

// Single use: a consumed invitation must not be replayable.
func TestAcceptInvitationIsSingleUse(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		path := "/api/v1/membership/invitations/" + fixtures.MembershipInvitationTokenPlain + "/accept"
		payload := test.GenericPayload{"password": "a-sufficiently-long-passphrase"}

		first := test.PerformRequest(t, s, "POST", path, payload, nil)
		require.Equal(t, http.StatusOK, first.Result().StatusCode)

		second := test.PerformRequest(t, s, "POST", path, payload, nil)
		assert.Equal(t, http.StatusNotFound, second.Result().StatusCode,
			"a consumed invitation must no longer match")
	})
}

// Not-found, not-approved and expired must be reported identically, or the
// endpoint becomes an invitation oracle.
func TestAcceptInvitationDoesNotDistinguishFailureReasons(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		for _, token := range []string{
			"11111111-1111-4111-8111-111111111111", // never existed
			fixtures.UserAdminAccessToken1Plain,    // a real token, wrong kind
		} {
			res := test.PerformRequest(t, s, "POST",
				"/api/v1/membership/invitations/"+token+"/accept",
				test.GenericPayload{"password": "a-sufficiently-long-passphrase"}, nil)

			assert.Equal(t, http.StatusNotFound, res.Result().StatusCode)
		}
	})
}

// The invitation does not bypass the password policy.
func TestAcceptInvitationEnforcesPasswordPolicy(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		res := test.PerformRequest(t, s, "POST",
			"/api/v1/membership/invitations/"+fixtures.MembershipInvitationTokenPlain+"/accept",
			test.GenericPayload{"password": "short"}, nil)

		assert.Equal(t, http.StatusBadRequest, res.Result().StatusCode)
	})
}

// The full seam: approve through the admin API, then accept with the token that
// approval actually minted.
//
// Every other membership test starts from a fixture with a pre-set
// invitation_valid_until, which meant approval and acceptance were each covered
// but the JOIN between them was not -- and a zero InvitationTokenValidity (the
// config key was declared, used, and never read from the environment) mints
// invitations that are expired on arrival. This is the test that catches that.
func TestApproveThenAcceptUsesAFreshlyMintedInvitation(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		ctx := t.Context()

		// Apply.
		res := test.PerformRequest(t, s, "POST", "/api/v1/membership/applications", test.GenericPayload{
			"email":    "end-to-end@example.com",
			"fullName": "End To End",
		}, nil)
		require.Equal(t, http.StatusAccepted, res.Result().StatusCode)

		application, err := models.MembershipApplications(
			qm.Where("lower(email) = ?", "end-to-end@example.com"),
		).One(ctx, s.DB)
		require.NoError(t, err)

		// Approve through the service the admin handler calls, capturing the
		// plaintext token exactly as the invitation email would.
		approval, err := s.Membership.ApproveMembershipApplication(ctx, dto.ApproveMembershipRequest{
			ApplicationID: application.ID,
			ReviewerID:    fixtures.Fixtures().UserAdmin.ID,
		})
		require.NoError(t, err)
		require.NotEmpty(t, approval.InvitationToken)

		require.NoError(t, application.Reload(ctx, s.DB))
		require.True(t, application.InvitationValidUntil.Valid)
		assert.True(t, application.InvitationValidUntil.Time.After(time.Now()),
			"a freshly minted invitation must not already be expired (valid until %s)",
			application.InvitationValidUntil.Time)

		// Accept with that token.
		res = test.PerformRequest(t, s, "POST",
			"/api/v1/membership/invitations/"+approval.InvitationToken+"/accept",
			test.GenericPayload{"password": "a-sufficiently-long-passphrase"}, nil)
		require.Equal(t, http.StatusOK, res.Result().StatusCode)

		var session types.PostLoginResponse
		test.ParseResponseAndValidate(t, res, &session)
		assert.NotEmpty(t, session.AccessToken)

		user, err := models.Users(qm.Where("lower(username) = ?", "end-to-end@example.com")).One(ctx, s.DB)
		require.NoError(t, err)
		assert.True(t, user.Password.Valid)
	})
}
