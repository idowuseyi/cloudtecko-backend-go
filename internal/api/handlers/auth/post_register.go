package auth

import (
	"net/http"

	"github.com/go-openapi/swag/conv"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/api"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/data/dto"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/types"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util/url"
	"github.com/labstack/echo/v4"
)

func PostRegisterRoute(s *api.Server) *echo.Route {
	return s.Router.APIV1Auth.POST("/register", postRegisterHandler(s))
}

func postRegisterHandler(s *api.Server) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		log := util.LogFromContext(ctx)

		var body types.PostRegisterPayload
		if err := util.BindAndValidateBody(c, &body); err != nil {
			return err
		}

		username := dto.NewUsername(body.Username.String())

		result, err := s.Auth.Register(ctx, dto.RegisterRequest{
			Username: username,
			Password: conv.Value(body.Password),
		})
		if err != nil {
			log.Debug().Err(err).Msg("Failed to register user")
			return err
		}

		// S-020: an address that already has an account must produce the SAME
		// response as a fresh registration -- same status, same body. The only
		// signal goes by email, to the mailbox owner rather than to whoever made
		// the request. Note this falls through to the shared 202 below rather than
		// returning its own, precisely so the two cannot drift apart.
		//
		// Caveat: this fully closes the oracle only when registration requires
		// confirmation. With confirmation off, a genuinely new registration is
		// answered with a session (below), which an existing address cannot be
		// given -- and that difference is itself the signal. router.Init logs a
		// warning at startup in that configuration.
		if result.AccountAlreadyExists {
			if err := s.Mailer.SendAccountExists(ctx, username.String()); err != nil {
				log.Err(err).Msg("Failed to send account exists notice")
			}
		}

		if !result.AccountAlreadyExists && !result.RequiresConfirmation {
			loginResult, err := s.Auth.Login(ctx, dto.LoginRequest{
				Username: username,
				Password: conv.Value(body.Password),
			})
			if err != nil {
				log.Debug().Err(err).Msg("Failed to authenticate user after registration")
				return err
			}

			return util.ValidateAndReturn(c, http.StatusOK, loginResult.ToTypes())
		}

		if result.ConfirmationToken.Valid {
			confirmationLink, err := url.ConfirmationDeeplinkURL(s.Config, result.ConfirmationToken.String)
			if err != nil {
				log.Debug().Err(err).Msg("Failed to generate confirmation link")
				return err
			}

			if err := s.Mailer.SendAccountConfirmation(ctx, username.String(), dto.ConfirmatioNotificationPayload{
				ConfirmationLink: confirmationLink.String(),
			}); err != nil {
				log.Debug().Err(err).Msg("Failed to send confirmation email")
				return err
			}
		}

		return util.ValidateAndReturn(c, http.StatusAccepted, &types.RegisterResponse{
			RequiresConfirmation: conv.Pointer(result.RequiresConfirmation),
		})
	}
}
