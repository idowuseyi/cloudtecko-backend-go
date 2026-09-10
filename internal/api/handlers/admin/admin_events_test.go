package admin_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/idowuseyi/cloudtecko-backend-go/internal/api"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/test"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/test/fixtures"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func adminAuth(t *testing.T) http.Header {
	t.Helper()

	return test.HeadersWithAuth(t, fixtures.UserAdminAccessToken1Plain)
}

func eventPayload(title string) test.GenericPayload {
	start := time.Now().Add(90 * 24 * time.Hour)

	return test.GenericPayload{
		"title":        title,
		"summary":      "A summary long enough to be useful.",
		"categorySlug": "workshop",
		"startAt":      start.Format(time.RFC3339),
		"endAt":        start.Add(4 * time.Hour).Format(time.RFC3339),
		"timezone":     "Africa/Lagos",
	}
}

// A newly created event must NOT be visible to the public. Publishing is a
// separate, explicit step so a half-written draft cannot reach the site.
func TestPostAdminEventCreatesUnpublished(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		res := test.PerformRequest(t, s, "POST", "/api/v1/admin/events", eventPayload("Brand New Workshop"), adminAuth(t))
		require.Equal(t, http.StatusCreated, res.Result().StatusCode)

		var created types.EventDetail
		test.ParseResponseAndValidate(t, res, &created)

		assert.Equal(t, "brand-new-workshop", *created.Slug, "slug is derived from the title")
		assert.False(t, *created.IsFeatured)

		// Not in the public listing yet.
		res = test.PerformRequest(t, s, "GET", "/api/v1/events/brand-new-workshop", nil, nil)
		assert.Equal(t, http.StatusNotFound, res.Result().StatusCode)
	})
}

func TestPostAdminEventSuffixesCollidingSlug(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		// The fixture already owns "aws-certification-workshop-lagos".
		res := test.PerformRequest(t, s, "POST", "/api/v1/admin/events",
			eventPayload("AWS Certification Workshop Lagos"), adminAuth(t))
		require.Equal(t, http.StatusCreated, res.Result().StatusCode)

		var created types.EventDetail
		test.ParseResponseAndValidate(t, res, &created)
		assert.Equal(t, "aws-certification-workshop-lagos-2", *created.Slug)

		// And again, to prove the series continues rather than colliding.
		res = test.PerformRequest(t, s, "POST", "/api/v1/admin/events",
			eventPayload("AWS Certification Workshop Lagos"), adminAuth(t))
		require.Equal(t, http.StatusCreated, res.Result().StatusCode)

		test.ParseResponseAndValidate(t, res, &created)
		assert.Equal(t, "aws-certification-workshop-lagos-3", *created.Slug)
	})
}

// A published slug is a permanent public address: renaming the event must not
// break every shared link pointing at it.
func TestPutAdminEventDoesNotChangeSlug(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		payload := eventPayload("A Completely Different Title")

		res := test.PerformRequest(t, s, "PUT",
			"/api/v1/admin/events/d2c4f2f0-2f40-4c30-9c5e-4b3d9eaf6001", payload, adminAuth(t))
		require.Equal(t, http.StatusOK, res.Result().StatusCode)

		var updated types.EventDetail
		test.ParseResponseAndValidate(t, res, &updated)

		assert.Equal(t, "A Completely Different Title", *updated.Title)
		assert.Equal(t, "aws-certification-workshop-lagos", *updated.Slug)
	})
}

func TestPostAdminEventRejectsEndBeforeStart(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		payload := eventPayload("Backwards In Time")
		start := time.Now().Add(90 * 24 * time.Hour)
		payload["startAt"] = start.Format(time.RFC3339)
		payload["endAt"] = start.Add(-2 * time.Hour).Format(time.RFC3339)

		res := test.PerformRequest(t, s, "POST", "/api/v1/admin/events", payload, adminAuth(t))
		assert.Equal(t, http.StatusBadRequest, res.Result().StatusCode)
	})
}

func TestPostAdminEventUnknownCategoryIs404(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		payload := eventPayload("No Such Category")
		payload["categorySlug"] = "does-not-exist"

		res := test.PerformRequest(t, s, "POST", "/api/v1/admin/events", payload, adminAuth(t))
		assert.Equal(t, http.StatusNotFound, res.Result().StatusCode)
	})
}

func TestPublishMakesEventPubliclyVisible(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		res := test.PerformRequest(t, s, "POST", "/api/v1/admin/events", eventPayload("Publish Me"), adminAuth(t))
		require.Equal(t, http.StatusCreated, res.Result().StatusCode)

		var created types.EventDetail
		test.ParseResponseAndValidate(t, res, &created)

		res = test.PerformRequest(t, s, "POST",
			"/api/v1/admin/events/"+created.ID.String()+"/publish",
			test.GenericPayload{"published": true}, adminAuth(t))
		require.Equal(t, http.StatusOK, res.Result().StatusCode)

		res = test.PerformRequest(t, s, "GET", "/api/v1/events/publish-me", nil, nil)
		assert.Equal(t, http.StatusOK, res.Result().StatusCode)
	})
}

// Featuring is a swap: the incumbent must be demoted in the same transaction, or
// the partial unique index would refuse the second write.
func TestFeatureSwapsAtomically(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		// azure-cloud-summit-2026 is published but not featured.
		res := test.PerformRequest(t, s, "POST",
			"/api/v1/admin/events/d2c4f2f0-2f40-4c30-9c5e-4b3d9eaf6002/feature",
			test.GenericPayload{"featured": true}, adminAuth(t))
		require.Equal(t, http.StatusOK, res.Result().StatusCode)

		res = test.PerformRequest(t, s, "GET", "/api/v1/events/featured", nil, nil)
		require.Equal(t, http.StatusOK, res.Result().StatusCode)

		var featured types.EventDetail
		test.ParseResponseAndValidate(t, res, &featured)
		assert.Equal(t, "azure-cloud-summit-2026", *featured.Slug, "the new event took the slot")

		// The previous incumbent is no longer featured.
		res = test.PerformRequest(t, s, "GET", "/api/v1/events/aws-certification-workshop-lagos", nil, nil)
		require.Equal(t, http.StatusOK, res.Result().StatusCode)

		var previous types.EventDetail
		test.ParseResponseAndValidate(t, res, &previous)
		assert.False(t, *previous.IsFeatured, "the incumbent was demoted")
	})
}

func TestFeatureUnpublishedIsRejected(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		res := test.PerformRequest(t, s, "POST",
			"/api/v1/admin/events/d2c4f2f0-2f40-4c30-9c5e-4b3d9eaf6003/feature",
			test.GenericPayload{"featured": true}, adminAuth(t))
		assert.Equal(t, http.StatusConflict, res.Result().StatusCode)
	})
}

// Unpublishing must clear the featured flag. The uniqueness index only covers
// PUBLISHED rows, so an unpublished-but-featured event leaves the slot free and
// would silently re-take it on republish -- two featured events, via a
// constraint that cannot see either.
func TestUnpublishClearsFeatured(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		res := test.PerformRequest(t, s, "POST",
			"/api/v1/admin/events/d2c4f2f0-2f40-4c30-9c5e-4b3d9eaf6001/publish",
			test.GenericPayload{"published": false}, adminAuth(t))
		require.Equal(t, http.StatusOK, res.Result().StatusCode)

		var updated types.EventDetail
		test.ParseResponseAndValidate(t, res, &updated)
		assert.False(t, *updated.IsFeatured)

		// Another event can now take the slot without a constraint violation.
		res = test.PerformRequest(t, s, "POST",
			"/api/v1/admin/events/d2c4f2f0-2f40-4c30-9c5e-4b3d9eaf6002/feature",
			test.GenericPayload{"featured": true}, adminAuth(t))
		assert.Equal(t, http.StatusOK, res.Result().StatusCode)
	})
}

func TestDeletePublishedEventIsRejected(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		res := test.PerformRequest(t, s, "DELETE",
			"/api/v1/admin/events/d2c4f2f0-2f40-4c30-9c5e-4b3d9eaf6001", nil, adminAuth(t))
		assert.Equal(t, http.StatusConflict, res.Result().StatusCode,
			"a published event must be archived, not deleted")
	})
}

func TestDeleteDraftEventSucceeds(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		res := test.PerformRequest(t, s, "DELETE",
			"/api/v1/admin/events/d2c4f2f0-2f40-4c30-9c5e-4b3d9eaf6003", nil, adminAuth(t))
		assert.Equal(t, http.StatusNoContent, res.Result().StatusCode)
	})
}

func TestArchiveHidesEventFromPublicListing(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		res := test.PerformRequest(t, s, "POST",
			"/api/v1/admin/events/d2c4f2f0-2f40-4c30-9c5e-4b3d9eaf6002/archive",
			test.GenericPayload{"archived": true}, adminAuth(t))
		require.Equal(t, http.StatusOK, res.Result().StatusCode)

		res = test.PerformRequest(t, s, "GET", "/api/v1/events/azure-cloud-summit-2026", nil, nil)
		assert.Equal(t, http.StatusNotFound, res.Result().StatusCode)
	})
}

// The FK is ON DELETE RESTRICT: refusing is correct, and cascading would delete
// published content.
func TestDeleteCategoryInUseIsRejected(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		res := test.PerformRequest(t, s, "DELETE",
			"/api/v1/admin/categories/b0a2f0de-0d2e-4a1e-9a3c-2f1b7c8d4e01", nil, adminAuth(t))
		assert.Equal(t, http.StatusConflict, res.Result().StatusCode)
	})
}

func TestPostAdminCategoryDuplicateSlugInSameScopeIsRejected(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		payload := test.GenericPayload{"scope": "event", "slug": "workshop", "name": "Workshop Again"}

		res := test.PerformRequest(t, s, "POST", "/api/v1/admin/categories", payload, adminAuth(t))
		assert.Equal(t, http.StatusConflict, res.Result().StatusCode)

		// The SAME slug in the OTHER scope is fine: uniqueness is (scope, slug).
		payload["scope"] = "resource"

		res = test.PerformRequest(t, s, "POST", "/api/v1/admin/categories", payload, adminAuth(t))
		assert.Equal(t, http.StatusCreated, res.Result().StatusCode)
	})
}
