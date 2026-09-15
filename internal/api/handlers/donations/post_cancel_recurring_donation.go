package donations

import (
	"net/http"

	"github.com/idowuseyi/cloudtecko-backend-go/internal/api"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/types/donations"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util"
	"github.com/labstack/echo/v4"
)

func PostCancelRecurringDonationRoute(s *api.Server) *echo.Route {
	return s.Router.APIV1Community.POST("/donations/:reference/cancel", postCancelRecurringDonationHandler(s))
}

func postCancelRecurringDonationHandler(s *api.Server) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()

		params := donations.NewPostCancelRecurringDonationRouteParams()
		if err := util.BindAndValidatePathAndQueryParams(c, &params); err != nil {
			return err
		}

		// Read from the request: the binding helpers cover path and query only,
		// so a declared header parameter is validated but never populated. An
		// absent token takes the same path as a wrong one, producing one 404
		// rather than a 400/404 split that would say which references exist.
		if err := s.Local.CancelRecurringDonation(ctx, params.Reference, c.Request().Header.Get(donationTokenHeader)); err != nil {
			return err
		}

		return c.NoContent(http.StatusNoContent)
	}
}
