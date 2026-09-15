package local_test

import (
	"strings"
	"testing"

	"github.com/idowuseyi/cloudtecko-backend-go/internal/data/local"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every value the alphabet renders must already be alphanumeric, so that nothing
// has to be substituted afterwards.
//
// This is the property that actually matters, and it is not the same as "the
// alphabet has no duplicate characters" -- the previous alphabet had none. It
// padded a 30-character confusable-free set to 32 with "!", which IS distinct
// from every other character, and then rewrote "!" to "7" AFTER encoding to keep
// references typeable. That fold is where two distinct 5-bit values became the
// same character. An alphabet needing no fold cannot have one.
func TestReferenceAlphabetNeedsNoSubstitution(t *testing.T) {
	t.Parallel()

	alphabet := local.ReferenceAlphabetForTest()

	require.Len(t, alphabet, 32, "the probe must render all 32 values")

	seen := map[rune]bool{}

	for _, char := range alphabet {
		assert.True(t, isReferenceChar(char),
			"value renders as %q, which is not alphanumeric and would need substituting", char)
		assert.False(t, seen[char], "character %q maps from more than one value", char)
		seen[char] = true
	}
}

func isReferenceChar(char rune) bool {
	return (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9')
}

// References must stay alphanumeric: they travel through the provider, appear in
// bank statements and are typed into support tools.
func TestReferenceIsAlphanumeric(t *testing.T) {
	t.Parallel()

	for range 50 {
		reference, err := local.NewPaymentReferenceForTest("don")
		require.NoError(t, err)

		suffix, found := strings.CutPrefix(reference, "cld_don_")
		require.True(t, found, "reference %q must carry the expected prefix", reference)

		for _, char := range suffix {
			assert.True(t, isReferenceChar(char), "reference %q contains %q", reference, char)
		}
	}
}

// Distinctness is the whole point: the reference is the key the provider's
// webhook uses to find the payment.
func TestReferencesDoNotRepeat(t *testing.T) {
	t.Parallel()

	seen := map[string]bool{}

	for range 1000 {
		reference, err := local.NewPaymentReferenceForTest("mch")
		require.NoError(t, err)
		require.False(t, seen[reference], "duplicate reference %q", reference)
		seen[reference] = true
	}
}
