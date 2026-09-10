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
)

func PostAdminResourceRoute(s *api.Server) *echo.Route {
	return s.Router.APIV1Admin.POST("/resources", postAdminResourceHandler(s))
}

func postAdminResourceHandler(s *api.Server) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		var body types.PostResourcePayload
		if err := util.BindAndValidateBody(c, &body); err != nil {
			return err
		}

		req := dto.ResourceWriteRequest{
			Title:              *body.Title,
			Summary:            *body.Summary,
			Body:               body.Body,
			Kind:               *body.Kind,
			CategorySlug:       *body.CategorySlug,
			HeroMediaID:        uuidToNullString(body.HeroMediaID),
			Author:             body.Author,
			ContentURL:         null.StringFromPtr(body.ContentURL),
			ReadingTimeMinutes: int(conv.Value(body.ReadingTimeMinutes)),
		}

		result, err := s.Local.CreateResource(ctx, req)
		if err != nil {
			return err
		}

		return util.ValidateAndReturn(c, http.StatusCreated, result.ToTypes())
	}
}
