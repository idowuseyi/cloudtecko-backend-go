package auth

import (
	"net/http"

	"github.com/go-openapi/swag/conv"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/api"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/auth"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/data/dto"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/types"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util"
	"github.com/labstack/echo/v4"
)

func PostChangePasswordRoute(s *api.Server) *echo.Route {
	return s.Router.APIV1Auth.POST("/change-password", postChangePasswordHandler(s))
}

func postChangePasswordHandler(s *api.Server) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		user := auth.UserFromEchoContext(c)
		log := util.LogFromContext(ctx)

		var body types.PostChangePasswordPayload
		if err := util.BindAndValidateBody(c, &body); err != nil {
			return err
		}

		result, err := s.Auth.UpdatePassword(ctx, dto.UpdatePasswordRequest{
			User:            *user,
			CurrentPassword: conv.Value(body.CurrentPassword),
			NewPassword:     conv.Value(body.NewPassword),
		})
		if err != nil {
			log.Debug().Err(err).Msg("Failed to update password")
			return err
		}

		return util.ValidateAndReturn(c, http.StatusOK, result.ToTypes())
	}
}
