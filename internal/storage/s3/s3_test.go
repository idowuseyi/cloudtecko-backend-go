package s3_test

import (
	"testing"

	"github.com/idowuseyi/cloudtecko-backend-go/internal/storage/s3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPublicURLPreservesPathSeparators(t *testing.T) {
	t.Parallel()

	store, err := s3.New(s3.Config{
		Endpoint:      "example.r2.cloudflarestorage.com",
		Bucket:        "media",
		PublicBaseURL: "https://media.cloudadore.test/",
	})
	require.NoError(t, err)

	// Separators must survive: escaping the whole key would collapse the path
	// into one flat, unresolvable segment.
	assert.Equal(t, "https://media.cloudadore.test/public/events/hero.jpg",
		store.PublicURL("public/events/hero.jpg"))

	// A leading slash must not double up against the trimmed base URL.
	assert.Equal(t, "https://media.cloudadore.test/public/hero.jpg",
		store.PublicURL("/public/hero.jpg"))

	// Characters that are unsafe WITHIN a segment are still escaped.
	assert.Equal(t, "https://media.cloudadore.test/public/a%20b%3Fc.jpg",
		store.PublicURL("public/a b?c.jpg"))
}

func TestNewRejectsIncompleteConfig(t *testing.T) {
	t.Parallel()

	_, err := s3.New(s3.Config{Bucket: "media"})
	require.Error(t, err, "endpoint is required")

	_, err = s3.New(s3.Config{Endpoint: "example.com"})
	require.Error(t, err, "bucket is required")
}
