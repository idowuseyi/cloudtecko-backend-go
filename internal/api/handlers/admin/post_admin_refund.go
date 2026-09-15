package admin

import (
	"net/http"

	"github.com/aarondl/null/v8"
	"github.com/go-openapi/swag/conv"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/api"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/api/middleware"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/auth"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/data/dto"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/types"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/types/admin"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util"
	"github.com/labstack/echo/v4"
)

// Refunds require RECENT authentication on top of the admin scope.
//
// AuthModeSecure re-checks last_authenticated_at against
// SERVER_AUTH_LAST_AUTHENTICATED_AT_THRESHOLD, so a stolen access token that is
// merely valid is not enough to move money back out. This is the operation where
// a 24-hour-old token should not suffice.
func PostAdminRefundRoute(s *api.Server) *echo.Route {
	return s.Router.APIV1Admin.POST("/payments/:id/refund", postAdminRefundHandler(s),
		middleware.AuthWithConfig(middleware.AuthConfig{
			S:      s,
			Mode:   middleware.AuthModeSecure,
			Scopes: []string{auth.ScopeAdmin.String()},
		}))
}

func postAdminRefundHandler(s *api.Server) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		actor := auth.UserFromEchoContext(c)

		params := admin.NewPostAdminRefundRouteParams()
		if err := util.BindAndValidatePathAndQueryParams(c, &params); err != nil {
			return err
		}

		var body types.PostRefundPayload
		if err := util.BindAndValidateBody(c, &body); err != nil {
			return err
		}

		result, err := s.Local.RefundPayment(ctx, dto.RefundRequest{
			PaymentID:   params.ID.String(),
			AmountKobo:  conv.Value(body.AmountKobo),
			Reason:      body.Reason,
			Restock:     conv.Value(body.Restock),
			ActorUserID: null.StringFrom(actor.ID),
			ActorEmail:  actor.Username.String,
			SourceIP:    c.RealIP(),
		})
		if err != nil {
			return err
		}

		return util.ValidateAndReturn(c, http.StatusOK, result.ToTypes())
	}
}
