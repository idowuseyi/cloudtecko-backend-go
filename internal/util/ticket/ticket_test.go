package ticket_test

import (
	"strings"
	"testing"

	"github.com/idowuseyi/cloudtecko-backend-go/internal/util/ticket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewProducesReadableUniqueCodes(t *testing.T) {
	t.Parallel()

	seen := map[string]struct{}{}

	for range 2000 {
		code, err := ticket.New()
		require.NoError(t, err)

		assert.Regexp(t, `^CLD-[A-Z2-9]{5}-[A-Z2-9]{5}$`, code)

		// Confusable characters must never appear: these codes are read aloud at
		// a door and typed by a volunteer.
		for _, forbidden := range []string{"I", "L", "O", "U", "0", "1"} {
			assert.NotContains(t, strings.TrimPrefix(code, "CLD-"), forbidden,
				"code %s contains the confusable character %s", code, forbidden)
		}

		_, duplicate := seen[code]
		require.False(t, duplicate, "code %s repeated within 2000 draws", code)
		seen[code] = struct{}{}
	}
}

func TestNormalizeAcceptsHowPeopleActuallyTypeCodes(t *testing.T) {
	t.Parallel()

	code, err := ticket.New()
	require.NoError(t, err)

	bare := strings.ReplaceAll(strings.TrimPrefix(code, "CLD-"), "-", "")

	variants := []struct {
		name  string
		input string
	}{
		{"exactly as issued", code},
		{"lower case", strings.ToLower(code)},
		{"no hyphens", strings.ReplaceAll(code, "-", "")},
		{"spaces instead of hyphens", strings.ReplaceAll(code, "-", " ")},
		{"without the prefix", bare},
		{"lower case, no prefix, spaced", strings.ToLower(bare[:5] + " " + bare[5:])},
		{"surrounded by whitespace", "  " + code + "  "},
	}

	for _, variant := range variants {
		t.Run(variant.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, code, ticket.Normalize(variant.input))
		})
	}
}

func TestNormalizeRejectsWhatIsNotACode(t *testing.T) {
	t.Parallel()

	for _, input := range []string{"", "CLD-", "TOO-SHORT", "CLD-ABCDE-ABCDEF", "!!!!!!!!!!"} {
		assert.Empty(t, ticket.Normalize(input), "input %q must not normalise to a code", input)
	}
}

// Dropped characters must not let a wrong code normalise onto a right one by
// coincidence: two distinct issued codes must stay distinct.
func TestNormalizeKeepsDistinctCodesDistinct(t *testing.T) {
	t.Parallel()

	first, err := ticket.New()
	require.NoError(t, err)

	second, err := ticket.New()
	require.NoError(t, err)

	require.NotEqual(t, first, second)
	assert.NotEqual(t, ticket.Normalize(first), ticket.Normalize(second))
}
