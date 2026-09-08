package auth_test

import (
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/idowuseyi/cloudtecko-backend-go/internal/api"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/test"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/test/fixtures"
	"github.com/stretchr/testify/require"
)

func TestGetCompleteRegister(t *testing.T) {
	test.WithTestServer(t, func(s *api.Server) {
		fix := fixtures.Fixtures()

		res := test.PerformRequest(t, s, "GET", fmt.Sprintf("/api/v1/auth/register?token=%s", fix.UserRequiresConfirmationConfirmationToken.Token), nil, nil)
		require.Equal(t, http.StatusOK, res.Result().StatusCode)

		response, err := io.ReadAll(res.Body)
		require.NoError(t, err)

		test.Snapshoter.SaveString(t, string(response))
	})
}
