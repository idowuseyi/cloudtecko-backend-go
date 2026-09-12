package donations

import (
	"net/http"

	"github.com/idowuseyi/cloudtecko-backend-go/internal/api"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/types/donations"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util"
	"github.com/labstack/echo/v4"
)

// donationTokenHeader carries the access token issued at checkout.
const donationTokenHeader = "X-Donation-Token"

func GetDonationStatusRoute(s *api.Server) *echo.Route {
	return s.Router.APIV1Community.GET("/donations/:reference", getDonationStatusHandler(s))
}

func getDonationStatusHandler(s *api.Server) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()

		params := donations.NewGetDonationStatusRouteParams()
		if err := util.BindAndValidatePathAndQueryParams(c, &params); err != nil {
			return err
		}

		// Read from the request rather than the bound params: this repo's binding
		// helpers cover path and query only, so a swagger-declared header
		// parameter is validated but never populated.
		//
		// A HEADER, never a query string: internal/api/middleware/logger.go logs
		// req.URL.String() unconditionally -- it is NOT gated by
		// SERVER_LOGGER_LOG_REQUEST_QUERY -- so a token in the query would land
		// verbatim in the request log, plus proxy logs and any Referer header.
		//
		// An absent token is passed through rather than short-circuited, so
		// "missing" and "wrong" produce the same 404. Returning 400 for one and
		// 404 for the other would say which references exist.
		result, err := s.Local.GetDonationStatus(ctx, params.Reference, c.Request().Header.Get(donationTokenHeader))
		if err != nil {
			return err
		}

		return util.ValidateAndReturn(c, http.StatusOK, result.ToTypes())
	}
}
