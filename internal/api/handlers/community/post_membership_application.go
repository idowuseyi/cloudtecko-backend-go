package community

import (
	"net/http"

	"github.com/go-openapi/swag/conv"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/api"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/data/dto"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/types"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util"
	"github.com/labstack/echo/v4"
)

func PostMembershipApplicationRoute(s *api.Server) *echo.Route {
	return s.Router.APIV1Community.POST("/membership/applications", postMembershipApplicationHandler(s))
}

func postMembershipApplicationHandler(s *api.Server) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()

		var body types.PostMembershipApplicationPayload
		if err := util.BindAndValidateBody(c, &body); err != nil {
			return err
		}

		if err := s.Local.SubmitMembershipApplication(ctx, dto.MembershipApplicationRequest{
			Email:        body.Email.String(),
			FullName:     *body.FullName,
			Motivation:   conv.Value(&body.Motivation),
			Organisation: conv.Value(&body.Organisation),
		}); err != nil {
			return err
		}

		// 202 for a new application, a duplicate and a previously rejected address
		// alike. The email differs; the response must not.
		return c.NoContent(http.StatusAccepted)
	}
}
