package password_test

import (
	"strings"
	"testing"

	"github.com/idowuseyi/cloudtecko-backend-go/internal/util/password"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testPolicy() password.Policy {
	return password.Policy{
		MinLength:   15,
		MaxLength:   500,
		Identifiers: []string{"user1@example.com", "user1", "cloudtecko"},
	}
}

func TestValidateRejects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		password string
		wantErr  error
	}{
		// Length is the primary control...
		{name: "Empty", password: "", wantErr: password.ErrTooShort},
		{name: "FourteenChars", password: "abcDEF123!@#gh", wantErr: password.ErrTooShort},
		{name: "TooLong", password: strings.Repeat("a", 501), wantErr: password.ErrTooLong},

		// ...but on its own it lets these through, which is the whole point of
		// the blocklist.
		{name: "AllSameCharacter", password: "aaaaaaaaaaaaaaa", wantErr: password.ErrRepetitive},
		{name: "TwoCharUnitRepeated", password: "abababababababab", wantErr: password.ErrRepetitive},
		{name: "ThreeCharUnitRepeated", password: "123123123123123", wantErr: password.ErrRepetitive},
		{name: "AscendingDigits", password: "123456789012345", wantErr: password.ErrSequential},
		{name: "AscendingLetters", password: "abcdefghijklmno", wantErr: password.ErrSequential},
		{name: "DescendingLetters", password: "ponmlkjihgfedcb", wantErr: password.ErrSequential},
		{name: "KeyboardRow", password: "qwertyuiopasdfgh", wantErr: password.ErrSequential},
		{name: "KeyboardRowReversed", password: "poiuytrewqlkjhgf", wantErr: password.ErrSequential},

		// Common values that are long enough to pass the length rule.
		{name: "CommonLongPassword", password: "passwordpassword", wantErr: password.ErrCommon},
		{name: "CommonBaseWithDigits", password: "password12345678", wantErr: password.ErrCommon},
		{name: "CommonBasePadded", password: "!!!!welcome!!!!!", wantErr: password.ErrCommon},

		// Context-specific values -- the first things an attacker tries.
		{name: "ContainsUsername", password: "user1-is-my-password", wantErr: password.ErrContainsIdentifier},
		{name: "ContainsServiceName", password: "cloudtecko-rocks-2026", wantErr: password.ErrContainsIdentifier},

		// Input hygiene.
		{name: "WhitespaceOnly", password: strings.Repeat(" ", 20), wantErr: password.ErrWhitespaceOnly},
		{name: "ControlCharacters", password: "abcdefghij\x00klmnop", wantErr: password.ErrControlCharacters},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := password.Validate(tt.password, testPolicy())
			require.Error(t, err, "expected %q to be rejected", tt.password)
			assert.ErrorIs(t, err, tt.wantErr)
		})
	}
}

func TestValidateAccepts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		password string
	}{
		{name: "Passphrase", password: "chair lamp rocket vinegar"},
		{name: "RandomLooking", password: "7Kq!vZ2mLp#9wXtR"},
		{name: "ExactlyMinimum", password: "chairlamprocket"},
		{name: "AllLowercaseNoDigits", password: "chairlamprocketvinegar"},
		{name: "UnicodePassphrase", password: "gjörðu svo vel að koma"},
		{name: "LongPassphrase", password: "chair lamp rocket vinegar harbour ladder mango quilt"},
		// NIST explicitly forbids composition rules, so a password with no digit,
		// no symbol and no uppercase must be accepted on its merits.
		{name: "NoCharacterClassVariety", password: "vinegar rocket chair"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.NoError(t, password.Validate(tt.password, testPolicy()))
		})
	}
}

// Length is counted in characters, not bytes. A user counting the characters
// they typed must reach the same number the validator does.
func TestValidateCountsRunesNotBytes(t *testing.T) {
	t.Parallel()

	// 15 characters, but 30 bytes in UTF-8.
	pw := strings.Repeat("é", 15)
	assert.Greater(t, len(pw), 15, "precondition: more bytes than characters")

	// Rejected for being repetitive, NOT for being too short.
	err := password.Validate(pw, testPolicy())
	require.Error(t, err)
	require.NotErrorIs(t, err, password.ErrTooShort)

	// 14 characters is short regardless of how many bytes it occupies.
	require.ErrorIs(t, password.Validate(strings.Repeat("é", 14)+"x", password.Policy{MinLength: 16, MaxLength: 500}), password.ErrTooShort)
}

// The same characters can arrive as different byte sequences depending on the
// keyboard and platform. Without normalization a user could set a password on
// one device and be unable to log in from another.
func TestNormalizeMakesEquivalentFormsIdentical(t *testing.T) {
	t.Parallel()

	precomposed := "pässword-rocket" // "ä" as a single codepoint
	decomposed := "pässword-rocket" // "a" + combining diaeresis

	require.NotEqual(t, precomposed, decomposed, "precondition: the byte sequences differ")
	assert.Equal(t, password.Normalize(precomposed), password.Normalize(decomposed),
		"NFKC must make the two forms compare equal")
}

func TestNormalizeIsIdempotent(t *testing.T) {
	t.Parallel()

	for _, pw := range []string{"plain-ascii-password", "pässword", "ﬁre-and-ice-forever"} {
		once := password.Normalize(pw)
		assert.Equal(t, once, password.Normalize(once))
	}
}

// A short identifier would reject far too much: refusing every password
// containing a two-letter local part is unusable.
func TestShortIdentifiersAreIgnored(t *testing.T) {
	t.Parallel()

	policy := password.Policy{MinLength: 15, MaxLength: 500, Identifiers: []string{"ab", "jo"}}
	assert.NoError(t, password.Validate("absolutely fine jolly password", policy))
}
