package wellknown_test

import (
	"path/filepath"
	"testing"

	"github.com/idowuseyi/cloudtecko-backend-go/internal/api"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/api/httperrors"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/config"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/test"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util"
	"github.com/labstack/echo/v4"
)

func TestGetAndroidWellKnown(t *testing.T) {
	config := config.DefaultServiceConfigFromEnv()
	config.Paths.AndroidAssetlinksFile = filepath.Join(util.GetProjectRootDir(), "test", "testdata", "android-assetlinks.json")

	testGetWellKnown(t, config, "/.well-known/assetlinks.json")
}

func TestGetAndroidWellKnownNotFound(t *testing.T) {
	config := config.DefaultServiceConfigFromEnv()
	config.Paths.AndroidAssetlinksFile = ""

	test.WithTestServerConfigurable(t, config, func(s *api.Server) {
		res := test.PerformRequest(t, s, "GET", "/.well-known/assetlinks.json", nil, nil)
		test.RequireHTTPError(t, res, httperrors.NewFromEcho(echo.ErrNotFound))
	})
}
