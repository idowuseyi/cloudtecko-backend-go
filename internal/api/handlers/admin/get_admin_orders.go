package admin

import (
	"net/http"

	"github.com/aarondl/null/v8"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/api"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/types/admin"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util"
	"github.com/labstack/echo/v4"
)

func GetAdminOrdersRoute(s *api.Server) *echo.Route {
	return s.Router.APIV1Admin.GET("/orders", getAdminOrdersHandler(s))
}

func getAdminOrdersHandler(s *api.Server) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()

		params := admin.NewGetAdminOrdersRouteParams()
		if err := util.BindAndValidatePathAndQueryParams(c, &params); err != nil {
			return err
		}

		result, err := s.Local.ListOrders(ctx, null.StringFromPtr(params.Status))
		if err != nil {
			return err
		}

		return util.ValidateAndReturn(c, http.StatusOK, result.ToTypes())
	}
}
