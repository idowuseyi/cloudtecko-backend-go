package admin

import (
	"net/http"

	"github.com/go-openapi/swag/conv"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/api"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/data/dto"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/types"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util"
	"github.com/labstack/echo/v4"
)

func PostAdminCategoryRoute(s *api.Server) *echo.Route {
	return s.Router.APIV1Admin.POST("/categories", postAdminCategoryHandler(s))
}

func postAdminCategoryHandler(s *api.Server) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		var body types.PostCategoryPayload
		if err := util.BindAndValidateBody(c, &body); err != nil {
			return err
		}

		result, err := s.Local.CreateCategory(ctx, dto.CategoryWriteRequest{
			Scope:     *body.Scope,
			Slug:      *body.Slug,
			Name:      *body.Name,
			SortOrder: int(conv.Value(body.SortOrder)),
		})
		if err != nil {
			return err
		}

		return util.ValidateAndReturn(c, http.StatusCreated, result.ToTypes())
	}
}
