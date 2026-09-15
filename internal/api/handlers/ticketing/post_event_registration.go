package ticketing

import (
	"net/http"

	"github.com/aarondl/null/v8"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/api"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/auth"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/data/dto"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/types"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/types/ticketing"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util"
	"github.com/labstack/echo/v4"
)

func PostEventRegistrationRoute(s *api.Server) *echo.Route {
	return s.Router.APIV1Community.POST("/events/:slug/register", postEventRegistrationHandler(s))
}

func postEventRegistrationHandler(s *api.Server) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()

		params := ticketing.NewPostEventRegistrationRouteParams()
		if err := util.BindAndValidatePathAndQueryParams(c, &params); err != nil {
			return err
		}

		var body types.PostEventRegistrationPayload
		if err := util.BindAndValidateBody(c, &body); err != nil {
			return err
		}

		// Guest registration is the norm: membership is approval-gated, and an
		// event open to the public cannot require an approved account to attend.
		var userID null.String
		if user := auth.UserFromEchoContext(c); user != nil {
			userID = null.StringFrom(user.ID)
		}

		result, err := s.Local.RegisterForEvent(ctx, dto.EventRegistrationRequest{
			EventSlug: params.Slug,
			TierID:    body.TierID.String(),
			Email:     body.Email.String(),
			FullName:  *body.FullName,
			Quantity:  int(*body.Quantity),
			UserID:    userID,
		})
		if err != nil {
			return err
		}

		return util.ValidateAndReturn(c, http.StatusCreated, result.ToTypes())
	}
}
