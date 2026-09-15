package merch

import (
	"net/http"

	"github.com/aarondl/null/v8"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/api"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/auth"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/data/dto"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/types"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util"
	"github.com/labstack/echo/v4"
)

func PostCheckoutRoute(s *api.Server) *echo.Route {
	return s.Router.APIV1Community.POST("/checkout", postCheckoutHandler(s))
}

func postCheckoutHandler(s *api.Server) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()

		var body types.PostCheckoutPayload
		if err := util.BindAndValidateBody(c, &body); err != nil {
			return err
		}

		lines := make([]dto.CartLine, len(body.Lines))
		for i, line := range body.Lines {
			lines[i] = dto.CartLine{
				VariantID: line.VariantID.String(),
				Quantity:  int(*line.Quantity),
			}
		}

		// Guest checkout: membership is approval-gated, and requiring an approved
		// account to buy a hoodie would be absurd.
		var userID null.String
		if user := auth.UserFromEchoContext(c); user != nil {
			userID = null.StringFrom(user.ID)
		}

		result, err := s.Local.Checkout(ctx, dto.CheckoutRequest{
			Lines:            lines,
			Email:            body.Email.String(),
			FullName:         *body.FullName,
			Phone:            null.StringFromPtr(body.Phone),
			ShippingLine1:    null.StringFromPtr(body.ShippingLine1),
			ShippingLine2:    null.StringFromPtr(body.ShippingLine2),
			ShippingCity:     null.StringFromPtr(body.ShippingCity),
			ShippingState:    *body.ShippingState,
			ShippingCountry:  null.StringFromPtr(body.ShippingCountry),
			ShippingPostcode: null.StringFromPtr(body.ShippingPostcode),
			UserID:           userID,
		})
		if err != nil {
			// The per-line detail rides on the 409 so the client can mark exactly
			// which items to fix.
			if len(result.Problems) > 0 {
				return util.ValidateAndReturn(c, http.StatusConflict, result.ConflictToTypes())
			}

			return err
		}

		return util.ValidateAndReturn(c, http.StatusCreated, result.ToTypes())
	}
}
