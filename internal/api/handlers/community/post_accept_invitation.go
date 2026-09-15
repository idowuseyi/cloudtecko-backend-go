package community

import (
	"net/http"

	"github.com/idowuseyi/cloudtecko-backend-go/internal/api"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/data/dto"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/types"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util"
	"github.com/labstack/echo/v4"
)

func PostAcceptInvitationRoute(s *api.Server) *echo.Route {
	return s.Router.APIV1Community.POST("/membership/invitations/accept", postAcceptInvitationHandler(s))
}

func postAcceptInvitationHandler(s *api.Server) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()

		var body types.PostAcceptInvitationPayload
		if err := util.BindAndValidateBody(c, &body); err != nil {
			return err
		}

		result, err := s.Membership.AcceptMembershipInvitation(ctx, dto.AcceptMembershipRequest{
			InvitationToken: body.Token.String(),
			Password:        *body.Password,
		})
		if err != nil {
			return err
		}

		return util.ValidateAndReturn(c, http.StatusOK, result.ToTypes())
	}
}
