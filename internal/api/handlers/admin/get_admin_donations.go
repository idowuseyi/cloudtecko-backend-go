package admin

import (
	"net/http"

	"github.com/aarondl/null/v8"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/api"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/types/admin"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util"
	"github.com/labstack/echo/v4"
)

func GetAdminDonationsRoute(s *api.Server) *echo.Route {
	return s.Router.APIV1Admin.GET("/donations", getAdminDonationsHandler(s))
}

func getAdminDonationsHandler(s *api.Server) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()

		params := admin.NewGetAdminDonationsRouteParams()
		if err := util.BindAndValidatePathAndQueryParams(c, &params); err != nil {
			return err
		}

		result, err := s.Local.ListDonations(ctx, null.StringFromPtr(params.Status))
		if err != nil {
			return err
		}

		return util.ValidateAndReturn(c, http.StatusOK, result.ToTypes())
	}
}
