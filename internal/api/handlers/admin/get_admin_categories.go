package admin

import (
	"net/http"

	"github.com/idowuseyi/cloudtecko-backend-go/internal/api"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util"
	"github.com/labstack/echo/v4"
)

func GetAdminCategoriesRoute(s *api.Server) *echo.Route {
	return s.Router.APIV1Admin.GET("/categories", getAdminCategoriesHandler(s))
}

func getAdminCategoriesHandler(s *api.Server) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		result, err := s.Local.ListCategories(ctx)
		if err != nil {
			return err
		}

		return util.ValidateAndReturn(c, http.StatusOK, result.ToTypes())
	}
}
