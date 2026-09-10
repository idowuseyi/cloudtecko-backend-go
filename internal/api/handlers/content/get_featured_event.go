package content

import (
	"net/http"

	"github.com/idowuseyi/cloudtecko-backend-go/internal/api"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util"
	"github.com/labstack/echo/v4"
)

// GetFeaturedEventRoute must be registered BEFORE GetEventRoute's "/events/:slug"
// for echo to match "/events/featured" as a literal rather than as a slug. Echo's
// router does prefer a static segment over a param at the same position, so the
// order is defensive rather than load-bearing -- but AttachAllRoutes is generated
// alphabetically, which puts GetEvent before GetFeaturedEvent, and relying on
// router internals for correctness of a public URL is not worth the saving.
// "featured" is therefore also a reserved slug at creation time.
func GetFeaturedEventRoute(s *api.Server) *echo.Route {
	return s.Router.APIV1Content.GET("/events/featured", getFeaturedEventHandler(s))
}

func getFeaturedEventHandler(s *api.Server) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()

		result, err := s.Local.GetFeaturedEvent(ctx)
		if err != nil {
			return err
		}

		return util.ValidateAndReturn(c, http.StatusOK, result.ToTypes())
	}
}
