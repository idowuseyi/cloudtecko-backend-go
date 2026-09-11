package admin

import (
	"net/http"

	"github.com/idowuseyi/cloudtecko-backend-go/internal/api"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/auth"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/data/dto"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/types"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util"
	"github.com/labstack/echo/v4"
)

func PostAdminMediaPresignRoute(s *api.Server) *echo.Route {
	return s.Router.APIV1Admin.POST("/media/presign", postAdminMediaPresignHandler(s))
}

func postAdminMediaPresignHandler(s *api.Server) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		user := auth.UserFromEchoContext(c)

		var body types.PostMediaPresignPayload
		if err := util.BindAndValidateBody(c, &body); err != nil {
			return err
		}

		result, err := s.Local.PresignMediaUpload(ctx, dto.MediaPresignRequest{
			Filename:         *body.Filename,
			ContentType:      *body.ContentType,
			ByteSize:         *body.ByteSize,
			UploadedByUserID: user.ID,
		})
		if err != nil {
			return err
		}

		return util.ValidateAndReturn(c, http.StatusCreated, result.ToTypes())
	}
}
