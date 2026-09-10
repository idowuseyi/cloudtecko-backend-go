package admin

import (
	"net/http"

	"github.com/aarondl/null/v8"
	"github.com/go-openapi/swag/conv"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/api"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/data/dto"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/types"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util"
	"github.com/labstack/echo/v4"
	"time"
)

func PostAdminEventRoute(s *api.Server) *echo.Route {
	return s.Router.APIV1Admin.POST("/events", postAdminEventHandler(s))
}

func postAdminEventHandler(s *api.Server) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		var body types.PostEventPayload
		if err := util.BindAndValidateBody(c, &body); err != nil {
			return err
		}

		req := dto.EventWriteRequest{
			Title:        *body.Title,
			Summary:      *body.Summary,
			Body:         body.Body,
			CategorySlug: *body.CategorySlug,
			HeroMediaID:  uuidToNullString(body.HeroMediaID),
			StartAt:      time.Time(*body.StartAt),
			EndAt:        time.Time(*body.EndAt),
			Timezone:     *body.Timezone,
			IsOnline:     conv.Value(body.IsOnline),
			VenueName:    null.StringFromPtr(body.VenueName),
			VenueAddress: null.StringFromPtr(body.VenueAddress),
			MeetingURL:   null.StringFromPtr(body.MeetingURL),
		}

		result, err := s.Local.CreateEvent(ctx, req)
		if err != nil {
			return err
		}

		return util.ValidateAndReturn(c, http.StatusCreated, result.ToTypes())
	}
}
