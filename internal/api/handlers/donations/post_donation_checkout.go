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

func PostDonationCheckoutRoute(s *api.Server) *echo.Route {
	return s.Router.APIV1Community.POST("/donations/checkout", postDonationCheckoutHandler(s))
}

func postDonationCheckoutHandler(s *api.Server) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()

		var body types.PostDonationCheckoutPayload
		if err := util.BindAndValidateBody(c, &body); err != nil {
			return err
		}

		// Guest donations are the norm: membership is approval-gated, and
		// requiring an approved account to give money would be absurd. A signed-in
		// donor is linked to their account; everyone else is identified by email
		// alone.
		var userID null.String
		if user := auth.UserFromEchoContext(c); user != nil {
			userID = null.StringFrom(user.ID)
		}

		result, err := s.Local.StartDonation(ctx, dto.StartDonationRequest{
			Email:       body.Email.String(),
			AmountKobo:  *body.AmountKobo,
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
