package content_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/idowuseyi/cloudtecko-backend-go/internal/api"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/test"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetResourcesListsOnlyPublished(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		res := test.PerformRequest(t, s, "GET", "/api/v1/resources", nil, nil)
		require.Equal(t, http.StatusOK, res.Result().StatusCode)

		var response types.GalleryResponse
		test.ParseResponseAndValidate(t, res, &response)

		assert.Len(t, response.Items, 2)

		slugs := make([]string, 0, len(response.Items))
		for _, item := range response.Items {
			slugs = append(slugs, *item.ID)
		}

		assert.NotContains(t, slugs, "unpublished-draft-resource")
	})
}

// Resources carry no availability badge; only events do. A non-nil status here
// would mean the shared gallery projection had leaked an events-only concept.
func TestGetResourcesCarryNoStatus(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		res := test.PerformRequest(t, s, "GET", "/api/v1/resources", nil, nil)
		require.Equal(t, http.StatusOK, res.Result().StatusCode)

		var response types.GalleryResponse
		test.ParseResponseAndValidate(t, res, &response)

		require.NotEmpty(t, response.Items)

		for _, item := range response.Items {
			assert.Nil(t, item.Status)
		}
	})
}

func TestGetResourcesFullTextSearchRanksTitleAboveBody(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		// "serverless" is in one resource's TITLE and the other's BODY. The
		// setweight() weighting in the generated column must put the title match
		// first, or relevance ordering is not actually working.
		res := test.PerformRequest(t, s, "GET", "/api/v1/resources?q=serverless", nil, nil)
		require.Equal(t, http.StatusOK, res.Result().StatusCode)

		var response types.GalleryResponse
		test.ParseResponseAndValidate(t, res, &response)

		require.Len(t, response.Items, 2)
		assert.Equal(t, "serverless-security-protecting-your-functions", *response.Items[0].ID)
		assert.Equal(t, "kubernetes-deployment-zero-to-production", *response.Items[1].ID)
	})
}

func TestGetResourcesSearchNarrows(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		res := test.PerformRequest(t, s, "GET", "/api/v1/resources?q=kubernetes", nil, nil)
		require.Equal(t, http.StatusOK, res.Result().StatusCode)

		var response types.GalleryResponse
		test.ParseResponseAndValidate(t, res, &response)

		require.Len(t, response.Items, 1)
		assert.Equal(t, "kubernetes-deployment-zero-to-production", *response.Items[0].ID)
		assert.Equal(t, int64(1), *response.Total)
	})
}

// Prefix matching: the frontend's search box fires as the user types, so a
// partial word has to match.
func TestGetResourcesSearchMatchesPrefix(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		res := test.PerformRequest(t, s, "GET", "/api/v1/resources?q=kuber", nil, nil)
		require.Equal(t, http.StatusOK, res.Result().StatusCode)

		var response types.GalleryResponse
		test.ParseResponseAndValidate(t, res, &response)

		require.Len(t, response.Items, 1)
		assert.Equal(t, "kubernetes-deployment-zero-to-production", *response.Items[0].ID)
	})
}

// The reason SearchStringToTSQuery uses an allow-list. Each of these used to be
// able to reach to_tsquery as malformed syntax, which is a 500 on a public GET
// that any visitor can trigger. They must now degrade, never error.
func TestGetResourcesSearchNeverReturns500ForHostileInput(t *testing.T) {
	t.Parallel()

	hostile := []string{
		`foo\`,          // trailing backslash escapes the closing quote
		`\`,             // backslash alone
		`a|b`,           // tsquery OR operator
		`foo)`,          // unbalanced paren
		`!@#$%^&*()<->`, // operators only, no lexeme at all
		`'`,             // single quote
		`a <-> b`,       // phrase operator
		`:*`,            // bare prefix marker
	}

	test.WithTestServer(t, func(s *api.Server) {
		for _, q := range hostile {
			path := "/api/v1/resources?q=" + url.QueryEscape(q)

			res := test.PerformRequest(t, s, "GET", path, nil, nil)
			assert.Equal(t, http.StatusOK, res.Result().StatusCode, "hostile search %q must not error", q)
		}
	})
}

// Punctuation-only input carries no searchable term, so it degrades to "no
// filter" and returns everything rather than nothing or an error.
func TestGetResourcesSearchWithNoLexemeReturnsAll(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		res := test.PerformRequest(t, s, "GET", "/api/v1/resources?q="+url.QueryEscape("!!!"), nil, nil)
		require.Equal(t, http.StatusOK, res.Result().StatusCode)

		var response types.GalleryResponse
		test.ParseResponseAndValidate(t, res, &response)

		assert.Equal(t, int64(2), *response.Total)
	})
}

func TestGetResourceBySlug(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		res := test.PerformRequest(t, s, "GET", "/api/v1/resources/serverless-security-protecting-your-functions", nil, nil)
		require.Equal(t, http.StatusOK, res.Result().StatusCode)

		var response types.ResourceDetail
		test.ParseResponseAndValidate(t, res, &response)

		assert.Equal(t, "Serverless Security: Protecting Your Functions", *response.Title)
		assert.Equal(t, "Ada Obi", *response.Author)
		assert.Equal(t, "Blog", *response.Category)
		assert.Equal(t, int64(8), *response.ReadingTimeMinutes)
	})
}

func TestGetResourceBySlugNotFoundForUnpublished(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		res := test.PerformRequest(t, s, "GET", "/api/v1/resources/unpublished-draft-resource", nil, nil)
		assert.Equal(t, http.StatusNotFound, res.Result().StatusCode)
	})
}

// The nil-guard added to the parameter template must not disable validation when
// the parameter IS supplied -- a guard that silently accepted anything would be a
// worse bug than the panic it replaced.
func TestGetResourcesSearchTooLongIsRejected(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		tooLong := strings.Repeat("a", 201)

		res := test.PerformRequest(t, s, "GET", "/api/v1/resources?q="+tooLong, nil, nil)
		assert.Equal(t, http.StatusBadRequest, res.Result().StatusCode)

		atLimit := strings.Repeat("a", 200)

		res = test.PerformRequest(t, s, "GET", "/api/v1/resources?q="+atLimit, nil, nil)
		assert.Equal(t, http.StatusOK, res.Result().StatusCode)
	})
}

// perPage carries a maximum; exceeding it must still be rejected.
func TestGetResourcesPerPageOverMaximumIsRejected(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		res := test.PerformRequest(t, s, "GET", "/api/v1/resources?perPage=101", nil, nil)
		assert.Equal(t, http.StatusBadRequest, res.Result().StatusCode)
	})
}
