// nolint:revive
package common

import (
	"path/filepath"

	"github.com/idowuseyi/cloudtecko-backend-go/internal/api"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/api/middleware"
	"github.com/labstack/echo/v4"
)

// GetSwaggerRoute serves the generated api/swagger.yml at /swagger.yml.
//
// The route is always registered -- scripts/internal/handlers/check.go walks
// s.Router.Routes and dereferences each one, so it must never be nil -- but it
// responds 404 when SERVER_ECHO_ENABLE_PUBLIC_SWAGGER is false.
//
// The default is true, matching previous behaviour: the docker-compose swaggerui
// service reads this endpoint, so defaulting it off would break local
// development. Production deployments set it to false, since the full API
// specification (every route, parameter and error shape) is a useful map for an
// attacker and is served unauthenticated.
func GetSwaggerRoute(s *api.Server) *echo.Route {
	// ---
	// Serve generated swagger.yml file statically at /swagger.yml
	// hack: not attached to group - can go away after echo/group.go .File and .Static actually return the *echo.Route
	// see https://github.com/labstack/echo/issues/1595
	// return s.Router.Root.File("swagger.yml", filepath.Join(s.Config.Echo.APIBaseDirAbs, "swagger.yml"))
	// we explicitly enforce a no-cache directive on any requests to it.
	return s.Echo.File(
		"/swagger.yml",
		filepath.Join(s.Config.Paths.APIBaseDirAbs, "swagger.yml"),
		middleware.NoCache(),
		publicSwaggerGate(s),
	)
}

func publicSwaggerGate(s *api.Server) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if !s.Config.Echo.EnablePublicSwagger {
				return echo.ErrNotFound
			}

			return next(c)
		}
	}
}
