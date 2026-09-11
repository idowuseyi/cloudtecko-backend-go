package admin_test

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"strings"
	"testing"

	"github.com/idowuseyi/cloudtecko-backend-go/internal/api"
	storagemock "github.com/idowuseyi/cloudtecko-backend-go/internal/storage/mock"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/test"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// realPNG returns bytes that genuinely sniff as image/png, so the content check
// is exercised against real magic bytes rather than a hand-written header.
func realPNG(t *testing.T) []byte {
	t.Helper()

	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})

	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))

	return buf.Bytes()
}

func mockStore(t *testing.T, s *api.Server) *storagemock.Storage {
	t.Helper()

	store, ok := s.Storage.(*storagemock.Storage)
	require.True(t, ok, "tests must run against the mock storage provider")

	return store
}

// presign reserves an asset and returns its id and object key.
func presign(t *testing.T, s *api.Server, filename string) (string, string) {
	t.Helper()

	res := test.PerformRequest(t, s, "POST", "/api/v1/admin/media/presign", test.GenericPayload{
		"filename":    filename,
		"contentType": "image/png",
		"byteSize":    1024,
	}, adminAuth(t))
	require.Equal(t, http.StatusCreated, res.Result().StatusCode)

	var response types.MediaPresignResponse
	test.ParseResponseAndValidate(t, res, &response)

	assert.Contains(t, *response.UploadURL, "quarantine/",
		"uploads must land in quarantine, never straight into the public prefix")

	return response.ID.String(), strings.SplitN(strings.SplitN(*response.UploadURL, "/upload/", 2)[1], "?", 2)[0]
}

func complete(t *testing.T, s *api.Server, assetID string) *http.Response {
	t.Helper()

	return test.PerformRequest(t, s, "POST", "/api/v1/admin/media/"+assetID+"/complete", nil, adminAuth(t)).Result()
}

func TestMediaUploadHappyPath(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		assetID, key := presign(t, s, "hero.png")

		mockStore(t, s).PutForTest(key, realPNG(t), "image/png")

		res := complete(t, s, assetID)
		require.Equal(t, http.StatusOK, res.StatusCode)

		var media types.Media
		test.ParseResponseAndValidate(t, test.PerformRequest(t, s, "POST",
			"/api/v1/admin/media/"+assetID+"/complete", nil, adminAuth(t)), &media)

		assert.Equal(t, "ready", *media.Status)
		assert.Equal(t, "image/png", *media.ContentType)
		require.NotNil(t, media.PublicURL)
		assert.Contains(t, *media.PublicURL, "public/")
		assert.NotContains(t, *media.PublicURL, "quarantine/",
			"a promoted object must no longer be addressed under quarantine")
	})
}

// The entire point of verify-on-complete: a presigned PUT cannot stop a client
// sending something other than what it declared.
func TestMediaCompleteRejectsContentTypeLie(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		assetID, key := presign(t, s, "totally-an-image.png")

		// Declared image/png, actually an HTML document -- the classic stored-XSS
		// payload shape.
		mockStore(t, s).PutForTest(key,
			[]byte("<html><body><script>alert(document.domain)</script></body></html>"),
			"image/png")

		res := complete(t, s, assetID)
		assert.Equal(t, http.StatusBadRequest, res.StatusCode,
			"the DETECTED type must decide, not the declared one")

		assert.NotContains(t, mockStore(t, s).Keys(), key,
			"a rejected upload must be deleted, not left as free storage")
	})
}

// SVG genuinely IS image/svg+xml, so no MIME check can catch it -- it has to be
// excluded from the allow-list outright.
func TestMediaCompleteRejectsSVG(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		res := test.PerformRequest(t, s, "POST", "/api/v1/admin/media/presign", test.GenericPayload{
			"filename":    "logo.svg",
			"contentType": "image/svg+xml",
			"byteSize":    512,
		}, adminAuth(t))

		assert.Equal(t, http.StatusBadRequest, res.Result().StatusCode,
			"SVG is an XSS vector that passes every MIME check, so it is not allowed at all")
	})
}

func TestMediaCompleteRejectsMissingUpload(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		// Presigned, but the client never PUT anything.
		assetID, _ := presign(t, s, "never-sent.png")

		res := complete(t, s, assetID)
		assert.Equal(t, http.StatusBadRequest, res.StatusCode)
	})
}

// A presigned PUT cannot enforce a size limit, so the real size is only known
// after the fact.
func TestMediaCompleteRejectsOversizedUpload(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		assetID, key := presign(t, s, "small.png")

		oversized := make([]byte, s.Config.Storage.MaxUploadBytes+1)
		copy(oversized, realPNG(t))
		mockStore(t, s).PutForTest(key, oversized, "image/png")

		res := complete(t, s, assetID)
		assert.Equal(t, http.StatusBadRequest, res.StatusCode,
			"the declared size was within the limit; the stored size was not")
	})
}

func TestMediaPresignRejectsOversizedDeclaration(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		res := test.PerformRequest(t, s, "POST", "/api/v1/admin/media/presign", test.GenericPayload{
			"filename":    "huge.png",
			"contentType": "image/png",
			"byteSize":    s.Config.Storage.MaxUploadBytes + 1,
		}, adminAuth(t))

		assert.Equal(t, http.StatusBadRequest, res.Result().StatusCode)
	})
}

// Clients retry. A second completion must report the asset as it stands rather
// than trying to promote an object that is no longer in quarantine.
func TestMediaCompleteIsIdempotent(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		assetID, key := presign(t, s, "hero.png")
		mockStore(t, s).PutForTest(key, realPNG(t), "image/png")

		first := complete(t, s, assetID)
		require.Equal(t, http.StatusOK, first.StatusCode)

		second := complete(t, s, assetID)
		assert.Equal(t, http.StatusOK, second.StatusCode, "completing twice must be safe")
	})
}

// Path traversal in a client-supplied filename must not escape the asset's
// own prefix.
func TestMediaPresignSanitisesFilename(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		_, key := presign(t, s, "../../../etc/passwd")

		assert.True(t, strings.HasPrefix(key, "quarantine/"), "key must stay under quarantine, got %q", key)
		assert.NotContains(t, key, "..")
		assert.Contains(t, key, "passwd", "the base name survives, the traversal does not")
	})
}

func TestMediaCompleteUnknownAssetIs404(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		res := complete(t, s, "c1b3f1ef-1e3f-4b2f-8b4d-3a2c8d9e5f99")
		assert.Equal(t, http.StatusNotFound, res.StatusCode)
	})
}
