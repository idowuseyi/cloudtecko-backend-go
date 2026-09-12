package donations

import (
	"net/http"

	"github.com/idowuseyi/cloudtecko-backend-go/internal/api"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util"
	"github.com/labstack/echo/v4"
)

func GetDonationTiersRoute(s *api.Server) *echo.Route {
	return s.Router.APIV1Content.GET("/donations/tiers", getDonationTiersHandler(s))
}

func getDonationTiersHandler(s *api.Server) echo.HandlerFunc {
	return func(c echo.Context) error {
		result, err := s.Local.ListDonationTiers(c.Request().Context())
		if err != nil {
			return err
		}

		return util.ValidateAndReturn(c, http.StatusOK, result.ToTypes())
	}
}
