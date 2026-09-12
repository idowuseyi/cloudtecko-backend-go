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
	ErrNotFoundMedia              = NewHTTPError(http.StatusNotFound, types.PublicHTTPErrorTypeGeneric, "The media asset does not exist.")
	ErrBadRequestUploadTooLarge   = NewHTTPError(http.StatusBadRequest, types.PublicHTTPErrorTypeGeneric, "The upload exceeds the maximum allowed size.")
	ErrBadRequestUploadMissing    = NewHTTPError(http.StatusBadRequest, types.PublicHTTPErrorTypeGeneric, "No uploaded object was found for this media asset.")

	// ErrBadRequestUnsupportedMediaType is returned for a DETECTED type outside
	// the allow-list, not merely a declared one.
	ErrBadRequestUnsupportedMediaType = NewHTTPError(http.StatusBadRequest, types.PublicHTTPErrorTypeGeneric, "That file type is not allowed.")

	ErrNotFoundApplication           = NewHTTPError(http.StatusNotFound, types.PublicHTTPErrorTypeGeneric, "The application does not exist.")
	ErrConflictApplicationNotPending = NewHTTPError(http.StatusConflict, types.PublicHTTPErrorTypeGeneric, "The application has already been reviewed.")

	// ErrNotFoundInvitationInvalid covers not-found, not-approved and expired
	// alike: distinguishing them would tell a caller which invitations exist.
	ErrNotFoundInvitationInvalid = NewHTTPError(http.StatusNotFound, types.PublicHTTPErrorTypeGeneric, "That invitation is not valid.")

	ErrConflictInvalidStateTransition = NewHTTPError(http.StatusConflict, types.PublicHTTPErrorTypeGeneric, "That change is not valid from the current state.")
	ErrNotFoundPayment                = NewHTTPError(http.StatusNotFound, types.PublicHTTPErrorTypeGeneric, "The payment does not exist.")

	// ErrNotFoundDonation covers an unknown reference AND a wrong access token,
	// so the endpoint cannot be used to discover which references exist.
	ErrNotFoundDonation          = NewHTTPError(http.StatusNotFound, types.PublicHTTPErrorTypeGeneric, "That donation was not found.")
	ErrBadRequestDonationAmount  = NewHTTPError(http.StatusBadRequest, types.PublicHTTPErrorTypeGeneric, "That donation amount is outside the permitted range.")
	ErrBadGatewayPaymentProvider = NewHTTPError(http.StatusBadGateway, types.PublicHTTPErrorTypeGeneric, "The payment provider could not be reached. No money has moved.")
)
