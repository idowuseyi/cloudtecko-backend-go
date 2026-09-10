package admin

import (
	"net/http"

	"github.com/idowuseyi/cloudtecko-backend-go/internal/api"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/types/admin"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util"
	"github.com/labstack/echo/v4"
)

func DeleteAdminCategoryRoute(s *api.Server) *echo.Route {
	return s.Router.APIV1Admin.DELETE("/categories/:id", deleteAdminCategoryHandler(s))
}

func deleteAdminCategoryHandler(s *api.Server) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		params := admin.NewDeleteAdminCategoryRouteParams()
		if err := util.BindAndValidatePathAndQueryParams(c, &params); err != nil {
			return err
		}

		if err := s.Local.DeleteCategory(ctx, params.ID.String()); err != nil {
			return err
		}

		return c.NoContent(http.StatusNoContent)
	}
}
