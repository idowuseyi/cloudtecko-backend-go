package ticketing

import (
	"net/http"

	"github.com/idowuseyi/cloudtecko-backend-go/internal/api"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/types/ticketing"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util"
	"github.com/labstack/echo/v4"
)

func GetEventTicketTiersRoute(s *api.Server) *echo.Route {
	return s.Router.APIV1Content.GET("/events/:slug/tiers", getEventTicketTiersHandler(s))
}

func getEventTicketTiersHandler(s *api.Server) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()

		params := ticketing.NewGetEventTicketTiersRouteParams()
		if err := util.BindAndValidatePathAndQueryParams(c, &params); err != nil {
			return err
		}

		result, err := s.Local.ListTicketTiers(ctx, params.Slug)
		if err != nil {
			return err
		}

		return util.ValidateAndReturn(c, http.StatusOK, result.ToTypes())
	}
}
