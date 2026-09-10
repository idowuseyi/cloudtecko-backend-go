package admin

import (
	"net/http"

	"github.com/aarondl/null/v8"
	"github.com/go-openapi/swag/conv"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/api"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/data/dto"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/types"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/types/admin"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util"
	"github.com/labstack/echo/v4"
	"time"
)

func PutAdminEventRoute(s *api.Server) *echo.Route {
	return s.Router.APIV1Admin.PUT("/events/:id", putAdminEventHandler(s))
}

func putAdminEventHandler(s *api.Server) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		params := admin.NewPutAdminEventRouteParams()
		if err := util.BindAndValidatePathAndQueryParams(c, &params); err != nil {
			return err
		}

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

		result, err := s.Local.UpdateEvent(ctx, params.ID.String(), req)
		if err != nil {
			return err
		}

		return util.ValidateAndReturn(c, http.StatusOK, result.ToTypes())
	}
}
