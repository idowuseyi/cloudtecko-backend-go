package content

import (
	"net/http"

	"github.com/aarondl/null/v8"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/api"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/data/dto"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/types/content"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util"
	"github.com/labstack/echo/v4"
)

func GetResourcesRoute(s *api.Server) *echo.Route {
	return s.Router.APIV1Content.GET("/resources", getResourcesHandler(s))
}

func getResourcesHandler(s *api.Server) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()

		params := content.NewGetResourcesRouteParams()
		if err := util.BindAndValidatePathAndQueryParams(c, &params); err != nil {
			return err
		}

		result, err := s.Local.ListResources(ctx, dto.ContentListRequest{
			Search:       null.StringFromPtr(params.Q),
			CategorySlug: null.StringFromPtr(params.Category),
			Page:         *params.Page,
			PerPage:      *params.PerPage,
		})
		if err != nil {
			return err
		}

		return util.ValidateAndReturn(c, http.StatusOK, result.ToTypes())
	}
}
