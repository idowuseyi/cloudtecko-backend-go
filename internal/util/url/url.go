package url

import (
	"fmt"
	"net/url"
	"path"

	"github.com/idowuseyi/cloudtecko-backend-go/internal/config"
)

const (
	queryParamToken         = "token"
	accountConfirmationPath = "/api/v1/auth/register"
)

func PasswordResetDeeplinkURL(config config.Server, token string) (*url.URL, error) {
	u, err := url.Parse(config.Frontend.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse the base URL: %w", err)
	}

	u.Path = path.Join(u.Path, config.Frontend.PasswordResetEndpoint)

	q := u.Query()
	q.Set(queryParamToken, token)
	u.RawQuery = q.Encode()

	return u, nil
}

func ConfirmationDeeplinkURL(config config.Server, token string) (*url.URL, error) {
	u, err := url.Parse(config.Echo.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse the base URL: %w", err)
	}

	u.Path = path.Join(u.Path, accountConfirmationPath)

	q := u.Query()
	q.Set(queryParamToken, token)
	u.RawQuery = q.Encode()

	return u, nil
}

func ConfirmationRequestURL(config config.Server, token string) (*url.URL, error) {
	u, err := url.Parse(config.Echo.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse the base URL: %w", err)
	}

	u.Path = path.Join(u.Path, accountConfirmationPath, token)

	return u, nil
}

// MembershipInvitationDeeplinkURL builds the link mailed to an approved applicant.
//
// It points at the FRONTEND, not at the API: accepting an invitation requires
// choosing a password, so the link must land on a page that can collect one. A
// link straight to the API endpoint would be a bare POST target with nowhere to
// type.
func MembershipInvitationDeeplinkURL(config config.Server, token string) (*url.URL, error) {
	u, err := url.Parse(config.Frontend.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse the base URL: %w", err)
	}

	u.Path = path.Join(u.Path, config.Frontend.MembershipInvitationEndpoint)

	q := u.Query()
	q.Set(queryParamToken, token)
	u.RawQuery = q.Encode()

	return u, nil
}
