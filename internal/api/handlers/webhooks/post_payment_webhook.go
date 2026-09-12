package webhooks

import (
	"errors"
	"io"
	"net/http"

	"github.com/idowuseyi/cloudtecko-backend-go/internal/api"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/payment"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/payment/paystack"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util"
	"github.com/labstack/echo/v4"
)

// maxWebhookBody bounds what will be read before signature verification.
//
// The body has to be buffered whole to compute the HMAC, and at this point it is
// entirely unauthenticated input, so the read must be capped. Provider payloads
// are a few kilobytes; 1 MiB is generous.
const maxWebhookBody = 1 << 20

func PostPaymentWebhookRoute(s *api.Server) *echo.Route {
	return s.Router.Webhooks.POST("/payments", postPaymentWebhookHandler(s))
}

func postPaymentWebhookHandler(s *api.Server) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		log := util.LogFromContext(ctx)

		// The RAW bytes, as received. This is load-bearing: the signature covers
		// exactly what the provider sent, and re-serialising parsed JSON changes
		// key order, whitespace and number formatting, so an HMAC computed over a
		// re-marshalled body can never match. util.BindAndValidateBody would also
		// consume the reader.
		rawBody, err := io.ReadAll(io.LimitReader(c.Request().Body, maxWebhookBody))
		if err != nil {
			log.Debug().Err(err).Msg("Failed to read webhook body")

			return echo.ErrBadRequest
		}

		event, err := s.Payment.ParseWebhook(rawBody, c.Request().Header.Get(paystack.SignatureHeader))
		if err != nil {
			switch {
			case errors.Is(err, payment.ErrSignatureInvalid):
				// 401 and NOTHING from the body in the log: an unverified payload is
				// attacker input, and echoing it would let anyone write arbitrary
				// content into our log aggregator.
				log.Warn().Msg("Rejected webhook with an invalid signature")

				return echo.ErrUnauthorized

			case errors.Is(err, payment.ErrUnsupportedEvent):
				// Authentic, but not an event we act on. 200 deliberately: a non-2xx
				// would make the provider retry it for up to 72 hours.
				log.Debug().Msg("Ignoring unsupported webhook event")

				return c.NoContent(http.StatusOK)

			default:
				log.Err(err).Msg("Failed to parse webhook")

				return echo.ErrBadRequest
			}
		}

		applied, err := s.Local.ProcessWebhook(ctx, event, s.Payment.Name())
		if err != nil {
			// Non-2xx ON PURPOSE. The provider retries, which is exactly what should
			// happen when we failed to record a money event -- and the unprocessed
			// row left behind is the operational alarm.
			log.Err(err).Str("idempotencyKey", event.IdempotencyKey).Msg("Failed to process webhook")

			return echo.ErrInternalServerError
		}

		log.Debug().
			Str("idempotencyKey", event.IdempotencyKey).
			Bool("applied", applied).
			Msg("Processed webhook")

		// 200 whether this delivery did the work or was a duplicate. A duplicate is
		// a success from the provider's point of view: the event IS handled.
		return c.NoContent(http.StatusOK)
	}
}
