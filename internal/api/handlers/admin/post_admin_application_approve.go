package admin

import (
	"net/http"

	"github.com/idowuseyi/cloudtecko-backend-go/internal/api"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/auth"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/data/dto"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/types"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/types/admin"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util/url"
	"github.com/labstack/echo/v4"
)

func PostAdminApplicationApproveRoute(s *api.Server) *echo.Route {
	return s.Router.APIV1Admin.POST("/applications/:id/approve", postAdminApplicationApproveHandler(s))
}

func postAdminApplicationApproveHandler(s *api.Server) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		log := util.LogFromContext(ctx)
		reviewer := auth.UserFromEchoContext(c)

		params := admin.NewPostAdminApplicationApproveRouteParams()
		if err := util.BindAndValidatePathAndQueryParams(c, &params); err != nil {
			return err
		}

		var body types.PostApplicationReviewPayload
		if err := util.BindAndValidateBody(c, &body); err != nil {
			return err
		}

		result, err := s.Membership.ApproveMembershipApplication(ctx, dto.ApproveMembershipRequest{
			ApplicationID: params.ID.String(),
			ReviewerID:    reviewer.ID,
			Note:          body.Note,
		})
		if err != nil {
			return err
		}

		// The invitation email is the ONLY place the plaintext token exists. A
		// send failure is logged but does not fail the request: the approval is
		// already committed, and returning an error would invite the admin to
		// approve again, which would mint a second token and invalidate the first.
		// Re-sending is a separate, explicit action.
		invitationLink, err := url.MembershipInvitationDeeplinkURL(s.Config, result.InvitationToken)
		if err != nil {
			log.Err(err).Msg("Failed to build the invitation link")

			return err
		}

		if err := s.Mailer.SendMembershipInvitation(ctx, result.Application.Email, result.Application.FullName,
			invitationLink.String(), s.Clock.Now().Add(s.Config.Auth.InvitationTokenValidity)); err != nil {
			log.Err(err).Str("applicationID", result.Application.ID).
				Msg("Approved the application but failed to send the invitation email")
		}

		return util.ValidateAndReturn(c, http.StatusOK, result.Application.ToTypes())
	}
}
