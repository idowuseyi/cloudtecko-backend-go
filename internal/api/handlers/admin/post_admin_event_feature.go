// The five admin toggle handlers (event publish/archive/feature, resource
// publish/archive) are deliberately near-identical. This repo registers one
// route per file so the AST-walking handler codegen can find each
// <Method><Name>Route function, and each toggle binds a different generated
// payload type and calls a different service method. Collapsing them behind a
// shared generic would save a dozen lines while hiding the route registrations
// from that codegen -- a bad trade.
//
//nolint:dupl // structural: one route per file, differing only in payload type and service call
package admin

import (
	"net/http"

	"github.com/idowuseyi/cloudtecko-backend-go/internal/api"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/types"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/types/admin"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util"
	"github.com/labstack/echo/v4"
)

func PostAdminEventFeatureRoute(s *api.Server) *echo.Route {
	return s.Router.APIV1Admin.POST("/events/:id/feature", postAdminEventFeatureHandler(s))
}

func postAdminEventFeatureHandler(s *api.Server) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		params := admin.NewPostAdminEventFeatureRouteParams()
		if err := util.BindAndValidatePathAndQueryParams(c, &params); err != nil {
			return err
		}

		var body types.FeaturePayload
		if err := util.BindAndValidateBody(c, &body); err != nil {
			return err
		}

		result, err := s.Local.SetEventFeatured(ctx, params.ID.String(), *body.Featured)
		if err != nil {
			return err
		}

		return util.ValidateAndReturn(c, http.StatusOK, result.ToTypes())
	}
}
