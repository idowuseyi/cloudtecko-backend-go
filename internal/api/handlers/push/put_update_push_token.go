package push

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

func PutUpdatePushTokenRoute(s *api.Server) *echo.Route {
	return s.Router.APIV1Push.PUT("/token", putUpdatePushTokenHandler(s))
}

func putUpdatePushTokenHandler(s *api.Server) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		user := auth.UserFromEchoContext(c)
		log := util.LogFromContext(ctx)

		var body types.PutUpdatePushTokenPayload
		if err := util.BindAndValidateBody(c, &body); err != nil {
			return err
		}

		err := s.Local.UpdatePushToken(ctx, dto.UpdatePushTokenRequest{
			User:          *user,
			Token:         conv.Value(body.NewToken),
			Provider:      conv.Value(body.Provider),
			ExistingToken: null.StringFromPtr(body.OldToken),
		})
		if err != nil {
			log.Debug().Err(err).Msg("Failed to update push token")
			return err
		}

		return c.String(http.StatusOK, "Success")
	}
}
