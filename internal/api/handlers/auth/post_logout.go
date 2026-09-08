package auth

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

func PostLogoutRoute(s *api.Server) *echo.Route {
	return s.Router.APIV1Auth.POST("/logout", postLogoutHandler(s))
}

func postLogoutHandler(s *api.Server) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		log := util.LogFromContext(ctx)

		var body types.PostLogoutPayload
		if err := util.BindAndValidateBody(c, &body); err != nil {
			return err
		}

		request := dto.LogoutRequest{
			AccessToken: *auth.AccessTokenFromEchoContext(c),
		}

		if len(body.RefreshToken.String()) > 0 {
			request.RefreshToken = null.StringFrom(body.RefreshToken.String())
		}

		err := s.Auth.Logout(ctx, request)
		if err != nil {
			log.Debug().Err(err).Msg("Failed to logout user")
			return err
		}

		return c.NoContent(http.StatusNoContent)
	}
}
