package slug_test

import (
	"strings"
	"testing"

	"github.com/idowuseyi/cloudtecko-backend-go/internal/util/slug"
	"github.com/stretchr/testify/assert"
)

func TestMake(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		title string
		want  string
	}{
		{"simple", "AWS Certification Workshop", "aws-certification-workshop"},
		{"colon and punctuation", "Serverless Security: Protecting Your Functions", "serverless-security-protecting-your-functions"},
		{"ampersand collapses", "Cloud Security & Compliance Masterclass", "cloud-security-compliance-masterclass"},
		{"leading and trailing junk", "  ...Hello World!!  ", "hello-world"},
		{"repeated separators collapse", "a---b___c   d", "a-b-c-d"},
		{"accents fold to ascii", "Café Déjà Vu", "cafe-deja-vu"},
		{"already a slug", "already-a-slug", "already-a-slug"},
		{"digits survive", "Kubernetes 1.31 Release", "kubernetes-1-31-release"},
		// A title with nothing slug-able must still produce a routable URL.
		{"no alphanumerics", "!!! ??? ...", slug.Fallback},
		{"emoji only", "🎉🎉🎉", slug.Fallback},
		// A slug equal to a sibling route would be unreachable.
		{"reserved word is suffixed", "Featured", "featured-1"},
		{"reserved new is suffixed", "New", "new-1"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, slug.Make(tc.title))
		})
	}
}

func TestMakeTruncatesWithoutTrailingHyphen(t *testing.T) {
	t.Parallel()

	got := slug.Make(strings.Repeat("word ", 40))

	assert.LessOrEqual(t, len(got), slug.MaxLength)
	assert.False(t, strings.HasSuffix(got, "-"), "truncation must not leave a trailing hyphen")
	assert.False(t, strings.HasPrefix(got, "-"))
}

func TestNextAvailable(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "post", slug.NextAvailable("post", map[string]struct{}{}))

	assert.Equal(t, "post-2", slug.NextAvailable("post", map[string]struct{}{
		"post": {},
	}))

	assert.Equal(t, "post-4", slug.NextAvailable("post", map[string]struct{}{
		"post": {}, "post-2": {}, "post-3": {},
	}))

	// Gaps are reused rather than skipped, so deleting post-2 lets the next
	// creation fill it instead of climbing forever.
	assert.Equal(t, "post-2", slug.NextAvailable("post", map[string]struct{}{
		"post": {}, "post-3": {},
	}))
}
