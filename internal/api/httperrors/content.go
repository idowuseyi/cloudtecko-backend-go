package httperrors

import (
	"net/http"

	"github.com/idowuseyi/cloudtecko-backend-go/internal/types"
)

var (
	ErrNotFoundEvent    = NewHTTPError(http.StatusNotFound, types.PublicHTTPErrorTypeGeneric, "The event does not exist.")
	ErrNotFoundResource = NewHTTPError(http.StatusNotFound, types.PublicHTTPErrorTypeGeneric, "The resource does not exist.")
)
