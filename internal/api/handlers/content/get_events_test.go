package content_test

import (
	"net/http"
	"testing"

	"github.com/idowuseyi/cloudtecko-backend-go/internal/api"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/test"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetEventsListsOnlyPublished(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		res := test.PerformRequest(t, s, "GET", "/api/v1/events", nil, nil)
		require.Equal(t, http.StatusOK, res.Result().StatusCode)

		var response types.GalleryResponse
		test.ParseResponseAndValidate(t, res, &response)

		// Two published; the draft and the archived one must not appear.
		assert.Len(t, response.Items, 2)
		require.NotNil(t, response.Total)
		assert.Equal(t, int64(2), *response.Total)

		slugs := make([]string, 0, len(response.Items))
		for _, item := range response.Items {
			require.NotNil(t, item.ID)
			slugs = append(slugs, *item.ID)
		}

		assert.NotContains(t, slugs, "unpublished-draft-event")
		assert.NotContains(t, slugs, "archived-past-event")
	})
}

func TestGetEventsFiltersByCategory(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		res := test.PerformRequest(t, s, "GET", "/api/v1/events?category=conference", nil, nil)
		require.Equal(t, http.StatusOK, res.Result().StatusCode)

		var response types.GalleryResponse
		test.ParseResponseAndValidate(t, res, &response)

		require.Len(t, response.Items, 1)
		assert.Equal(t, "azure-cloud-summit-2026", *response.Items[0].ID)
		assert.Equal(t, "Conference", *response.Items[0].Category)
	})
}

func TestGetEventsPaginates(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		res := test.PerformRequest(t, s, "GET", "/api/v1/events?perPage=1&page=1", nil, nil)
		require.Equal(t, http.StatusOK, res.Result().StatusCode)

		var first types.GalleryResponse
		test.ParseResponseAndValidate(t, res, &first)
		require.Len(t, first.Items, 1)
		// Total reflects ALL matches, not the page size -- otherwise the frontend
		// cannot render a page count.
		assert.Equal(t, int64(2), *first.Total)

		res = test.PerformRequest(t, s, "GET", "/api/v1/events?perPage=1&page=2", nil, nil)
		require.Equal(t, http.StatusOK, res.Result().StatusCode)

		var second types.GalleryResponse
		test.ParseResponseAndValidate(t, res, &second)
		require.Len(t, second.Items, 1)
		assert.NotEqual(t, *first.Items[0].ID, *second.Items[0].ID)
	})
}

func TestGetEventsReturnsCategoryTabs(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		res := test.PerformRequest(t, s, "GET", "/api/v1/events", nil, nil)
		require.Equal(t, http.StatusOK, res.Result().StatusCode)

		var response types.GalleryResponse
		test.ParseResponseAndValidate(t, res, &response)

		// Event-scoped categories only: the resource categories must not leak
		// into the events page's filter tabs.
		assert.Equal(t, []string{"Workshop", "Conference"}, response.Categories)
	})
}

func TestGetEventBySlug(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		res := test.PerformRequest(t, s, "GET", "/api/v1/events/aws-certification-workshop-lagos", nil, nil)
		require.Equal(t, http.StatusOK, res.Result().StatusCode)

		var response types.EventDetail
		test.ParseResponseAndValidate(t, res, &response)

		assert.Equal(t, "AWS Certification Workshop: Lagos Edition", *response.Title)
		// The wall-clock intent survives alongside the instant, which is the whole
		// reason the timezone is a column rather than an assumption.
		assert.Equal(t, "Africa/Lagos", *response.Timezone)
		assert.False(t, *response.IsOnline)
		require.NotNil(t, response.VenueName)
		assert.Equal(t, "Zone Tech Park", *response.VenueName)
	})
}

func TestGetEventBySlugNotFoundForUnpublished(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		res := test.PerformRequest(t, s, "GET", "/api/v1/events/unpublished-draft-event", nil, nil)
		assert.Equal(t, http.StatusNotFound, res.Result().StatusCode)

		res = test.PerformRequest(t, s, "GET", "/api/v1/events/archived-past-event", nil, nil)
		assert.Equal(t, http.StatusNotFound, res.Result().StatusCode)
	})
}

// The generated route list registers "/events/:slug" BEFORE "/events/featured"
// (AttachAllRoutes is emitted alphabetically). This asserts the literal segment
// still wins, so /events/featured is never swallowed as a slug lookup.
func TestGetFeaturedEventIsNotShadowedBySlugRoute(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		res := test.PerformRequest(t, s, "GET", "/api/v1/events/featured", nil, nil)
		require.Equal(t, http.StatusOK, res.Result().StatusCode)

		var response types.EventDetail
		test.ParseResponseAndValidate(t, res, &response)

		assert.Equal(t, "aws-certification-workshop-lagos", *response.Slug)
		assert.True(t, *response.IsFeatured)
	})
}
