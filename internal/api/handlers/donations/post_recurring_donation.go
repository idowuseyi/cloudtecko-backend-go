package donations

import (
	"net/http"

	"github.com/aarondl/null/v8"
	"github.com/go-openapi/swag/conv"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/api"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/auth"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/data/dto"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/types"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util"
	"github.com/labstack/echo/v4"
)

func PostRecurringDonationRoute(s *api.Server) *echo.Route {
	return s.Router.APIV1Community.POST("/donations/recurring", postRecurringDonationHandler(s))
}

func postRecurringDonationHandler(s *api.Server) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()

		var body types.PostRecurringDonationPayload
		if err := util.BindAndValidateBody(c, &body); err != nil {
			return err
		}

		var userID null.String
		if user := auth.UserFromEchoContext(c); user != nil {
			userID = null.StringFrom(user.ID)
		}

		result, err := s.Local.StartRecurringDonation(ctx, dto.StartRecurringDonationRequest{
			TierID:      body.TierID.String(),
			Email:       body.Email.String(),
			DisplayName: null.StringFromPtr(body.DisplayName),
			IsAnonymous: conv.Value(body.IsAnonymous),
			UserID:      userID,
		})
		if err != nil {
			return err
		}

		return util.ValidateAndReturn(c, http.StatusCreated, result.ToTypes())
	}
}
