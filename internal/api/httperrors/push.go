package httperrors

import (
	"net/http"

	"github.com/idowuseyi/cloudtecko-backend-go/internal/types"
)

var (
	ErrConflictPushToken    = NewHTTPError(http.StatusConflict, types.PublicHTTPErrorTypePUSHTOKENALREADYEXISTS, "The given token already exists.")
	ErrNotFoundOldPushToken = NewHTTPError(http.StatusNotFound, types.PublicHTTPErrorTypeOLDPUSHTOKENNOTFOUND, "The old push token does not exists. The new token was saved.")
)
