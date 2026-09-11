package community

import (
	"net/http"

	"github.com/idowuseyi/cloudtecko-backend-go/internal/api"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/data/dto"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/types"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util"
	"github.com/labstack/echo/v4"
)

func PostContactMessageRoute(s *api.Server) *echo.Route {
	return s.Router.APIV1Community.POST("/contact-messages", postContactMessageHandler(s))
}

func postContactMessageHandler(s *api.Server) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()

		var body types.PostContactMessagePayload
		if err := util.BindAndValidateBody(c, &body); err != nil {
			return err
		}

		if err := s.Local.SubmitContactMessage(ctx, dto.ContactMessageRequest{
			Name:    *body.Name,
			Email:   body.Email.String(),
			Subject: *body.Subject,
			Message: *body.Message,
			// c.RealIP() honours X-Forwarded-For, which is correct behind Render's
			// proxy and spoofable if this server is ever exposed directly. It is
			// truncated to a network prefix before storage either way, and is used
			// only for abuse triage -- never for authorisation.
			SourceIP:  c.RealIP(),
			UserAgent: c.Request().UserAgent(),
		}); err != nil {
			return err
		}

		// 202 regardless of whether the message was stored or dropped by the
		// per-sender throttle: see SubmitContactMessage.
		return c.NoContent(http.StatusAccepted)
	}
}
