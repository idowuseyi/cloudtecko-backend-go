package httperrors

import (
	"net/http"

	"github.com/idowuseyi/cloudtecko-backend-go/internal/types"
)

var (
	ErrNotFoundCategory           = NewHTTPError(http.StatusNotFound, types.PublicHTTPErrorTypeGeneric, "The category does not exist.")
	ErrConflictSlugTaken          = NewHTTPError(http.StatusConflict, types.PublicHTTPErrorTypeGeneric, "That slug is already taken.")
	ErrConflictFeatureUnpublished = NewHTTPError(http.StatusConflict, types.PublicHTTPErrorTypeGeneric, "An unpublished event cannot be featured.")
	ErrConflictDeletePublished    = NewHTTPError(http.StatusConflict, types.PublicHTTPErrorTypeGeneric, "A published event cannot be deleted; archive it instead.")
	ErrBadRequestEventTimeOrder   = NewHTTPError(http.StatusBadRequest, types.PublicHTTPErrorTypeGeneric, "The event end time must not precede its start time.")
	ErrConflictCategoryInUse      = NewHTTPError(http.StatusConflict, types.PublicHTTPErrorTypeGeneric, "The category is still referenced by content.")
)
