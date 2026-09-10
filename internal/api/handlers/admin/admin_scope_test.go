package admin_test

import (
	"net/http"
	"testing"

	"github.com/idowuseyi/cloudtecko-backend-go/internal/api"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/test"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/test/fixtures"
	"github.com/stretchr/testify/assert"
)

// adminRoutes is every route behind the admin group. The table is exhaustive on
// purpose: the failure mode of a mis-specified Scopes slice is that ONE route
// silently admits ordinary users, which a spot-check would miss.
var adminRoutes = []struct {
	method string
	path   string
}{
	{"GET", "/api/v1/admin/categories"},
	{"POST", "/api/v1/admin/categories"},
	{"DELETE", "/api/v1/admin/categories/b0a2f0de-0d2e-4a1e-9a3c-2f1b7c8d4e01"},
	{"POST", "/api/v1/admin/events"},
	{"PUT", "/api/v1/admin/events/d2c4f2f0-2f40-4c30-9c5e-4b3d9eaf6001"},
	{"DELETE", "/api/v1/admin/events/d2c4f2f0-2f40-4c30-9c5e-4b3d9eaf6001"},
	{"POST", "/api/v1/admin/events/d2c4f2f0-2f40-4c30-9c5e-4b3d9eaf6001/publish"},
	{"POST", "/api/v1/admin/events/d2c4f2f0-2f40-4c30-9c5e-4b3d9eaf6001/archive"},
	{"POST", "/api/v1/admin/events/d2c4f2f0-2f40-4c30-9c5e-4b3d9eaf6001/feature"},
	{"POST", "/api/v1/admin/resources"},
	{"PUT", "/api/v1/admin/resources/e3d5f3f1-3f51-4d41-8d6f-5c4eafb07001"},
	{"DELETE", "/api/v1/admin/resources/e3d5f3f1-3f51-4d41-8d6f-5c4eafb07001"},
	{"POST", "/api/v1/admin/resources/e3d5f3f1-3f51-4d41-8d6f-5c4eafb07001/publish"},
	{"POST", "/api/v1/admin/resources/e3d5f3f1-3f51-4d41-8d6f-5c4eafb07001/archive"},
}

// CheckUserScopes is OR, not AND. If the admin group ever listed auth.ScopeApp
// alongside auth.ScopeAdmin -- or listed nothing at all -- every authenticated
// user would become an admin. User1 holds only the app scope.
func TestAdminRoutesRejectNonAdminUser(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		for _, route := range adminRoutes {
			res := test.PerformRequest(t, s, route.method, route.path, test.GenericPayload{},
				test.HeadersWithAuth(t, fixtures.User1AccessToken1Plain))

			assert.Equal(t, http.StatusForbidden, res.Result().StatusCode,
				"%s %s must reject a user holding only the app scope", route.method, route.path)
		}
	})
}

func TestAdminRoutesRejectAnonymous(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		for _, route := range adminRoutes {
			res := test.PerformRequest(t, s, route.method, route.path, test.GenericPayload{}, nil)

			assert.Equal(t, http.StatusUnauthorized, res.Result().StatusCode,
				"%s %s must reject an anonymous caller", route.method, route.path)
		}
	})
}

// The mirror of the above: an admin must NOT be rejected by the scope check.
// 403 here would mean the guard is wrong in the other direction.
func TestAdminRoutesAdmitAdminUser(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		for _, route := range adminRoutes {
			res := test.PerformRequest(t, s, route.method, route.path, test.GenericPayload{},
				test.HeadersWithAuth(t, fixtures.UserAdminAccessToken1Plain))

			assert.NotEqual(t, http.StatusForbidden, res.Result().StatusCode,
				"%s %s must not reject an admin", route.method, route.path)
			assert.NotEqual(t, http.StatusUnauthorized, res.Result().StatusCode,
				"%s %s must not reject an admin", route.method, route.path)
		}
	})
}
