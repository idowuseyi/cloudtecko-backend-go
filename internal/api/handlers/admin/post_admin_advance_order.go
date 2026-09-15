package admin

import (
	"net/http"

	"github.com/aarondl/null/v8"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/api"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/auth"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/data/dto"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/types"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/types/admin"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util"
	"github.com/labstack/echo/v4"
)

func PostAdminAdvanceOrderRoute(s *api.Server) *echo.Route {
	return s.Router.APIV1Admin.POST("/orders/:id/:action", postAdminAdvanceOrderHandler(s))
}

func postAdminAdvanceOrderHandler(s *api.Server) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		actor := auth.UserFromEchoContext(c)

		params := admin.NewPostAdminAdvanceOrderRouteParams()
		if err := util.BindAndValidatePathAndQueryParams(c, &params); err != nil {
			return err
		}

		var body types.PostAdvanceOrderPayload
		if err := util.BindAndValidateBody(c, &body); err != nil {
			return err
		}

		result, err := s.Local.AdvanceOrder(ctx, dto.AdvanceOrderRequest{
			OrderID:           params.ID.String(),
			To:                params.Action,
			TrackingReference: null.StringFromPtr(body.TrackingReference),
			ActorUserID:       null.StringFrom(actor.ID),
			ActorEmail:        actor.Username.String,
			SourceIP:          c.RealIP(),
		})
		if err != nil {
			return err
		}

		return util.ValidateAndReturn(c, http.StatusOK, result.ToTypes())
	}
}
