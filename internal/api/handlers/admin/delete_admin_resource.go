package admin

import (
	"net/http"

	"github.com/idowuseyi/cloudtecko-backend-go/internal/api"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/types/admin"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util"
	"github.com/labstack/echo/v4"
)

func DeleteAdminResourceRoute(s *api.Server) *echo.Route {
	return s.Router.APIV1Admin.DELETE("/resources/:id", deleteAdminResourceHandler(s))
}

func deleteAdminResourceHandler(s *api.Server) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		params := admin.NewDeleteAdminResourceRouteParams()
		if err := util.BindAndValidatePathAndQueryParams(c, &params); err != nil {
			return err
		}

		if err := s.Local.DeleteResource(ctx, params.ID.String()); err != nil {
			return err
		}

		return c.NoContent(http.StatusNoContent)
	}
}
