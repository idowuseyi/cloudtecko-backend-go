// Package password validates and normalizes user-chosen passwords.
//
// The rules follow NIST SP 800-63B Rev 4 (finalized July 2025), which is
// deliberately different from the composition rules many services still use:
//
//   - Length is the primary control. 15 characters when a password is the only
//     authentication factor, which is the case here (there is no MFA).
//   - Verifiers SHALL compare a candidate against a blocklist of commonly-used,
//     expected or compromised values. Length alone does not stop
//     "aaaaaaaaaaaaaaa" or "123456789012345".
//   - Verifiers SHALL NOT impose composition rules ("must contain an uppercase
//     letter and a digit"). They push users toward predictable substitutions
//     like P@ssw0rd1 and measurably reduce entropy. There are deliberately no
//     character-class checks in this package.
//   - Verifiers SHALL NOT truncate, and SHALL accept at least 64 characters and
//     all printing ASCII plus space. Unicode SHOULD be accepted, and if it is,
//     it SHALL be normalized.
package password

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// Validation failures. They are distinguished so the caller can decide how much
// to tell the user; the handler maps all of them to one generic response.
var (
	ErrTooShort           = errors.New("password is too short")
	ErrTooLong            = errors.New("password is too long")
	ErrCommon             = errors.New("password is too common")
	ErrRepetitive         = errors.New("password is made up of repeated characters")
	ErrSequential         = errors.New("password is a sequence of adjacent characters")
	ErrContainsIdentifier = errors.New("password contains your username or the service name")
	ErrWhitespaceOnly     = errors.New("password is only whitespace")
	ErrControlCharacters  = errors.New("password contains control characters")
)

// Policy is the configurable part of the rules.
type Policy struct {
	// MinLength counts RUNES, not bytes: "pässwörtpässwört" is 16 characters to
	// a user and 18 bytes to Go, and the user's count is the one that matters.
	MinLength int
	MaxLength int
	// Identifiers are context-specific values a password must not contain --
	// the username, its local part, and the service name. NIST calls these out
	// explicitly because they are the first things an attacker tries.
	Identifiers []string
}

// Normalize prepares a password for hashing and for comparison.
//
// NFKC is applied because the same character can have several byte sequences:
// "é" may arrive as U+00E9 or as "e" + U+0301, and a user who sets a password on
// one keyboard and types it on another would otherwise be locked out. NIST
// requires normalization when Unicode is accepted.
//
// This MUST be applied identically when setting and when verifying a password,
// or the two will not agree. It is therefore done inside the hashing helpers
// rather than left to each call site.
func Normalize(pw string) string {
	return norm.NFKC.String(pw)
}

// Validate reports whether a password may be SET.
//
// It is never applied when verifying an existing password: a policy that rejects
// input before the credential check would lock out any user whose password
// predates it, rather than letting them authenticate and choose a new one.
func Validate(pw string, policy Policy) error {
	normalized := Normalize(pw)

	if length := utf8.RuneCountInString(normalized); length < policy.MinLength {
		return fmt.Errorf("%w: needs at least %d characters", ErrTooShort, policy.MinLength)
	} else if policy.MaxLength > 0 && length > policy.MaxLength {
		return fmt.Errorf("%w: at most %d characters", ErrTooLong, policy.MaxLength)
	}

	if strings.TrimSpace(normalized) == "" {
		return ErrWhitespaceOnly
	}

	// Control characters are rejected rather than stripped. Stripping would mean
	// the stored password differs from what the user typed, and a password that
	// silently loses characters is worse than one that is refused.
	for _, r := range normalized {
		if unicode.IsControl(r) {
			return ErrControlCharacters
		}
	}

	folded := strings.ToLower(normalized)

	if isRepetitive(folded) {
		return ErrRepetitive
	}

	if isSequential(folded) {
		return ErrSequential
	}

	if isCommon(folded) {
		return ErrCommon
	}

	for _, identifier := range policy.Identifiers {
		if containsIdentifier(folded, identifier) {
			return ErrContainsIdentifier
		}
	}

	return nil
}

// isRepetitive catches a password built from a short repeated unit:
// "aaaaaaaaaaaaaaa", "abababababababab", "123123123123123".
//
// A 16-character password made of a 2-character unit has the strength of the
// unit, not of its length -- which is exactly the hole a pure length rule leaves.
func isRepetitive(pw string) bool {
	runes := []rune(pw)

	// A unit longer than a third of the password is not meaningfully "repeated".
	for unitLen := 1; unitLen <= len(runes)/3; unitLen++ {
		if len(runes)%unitLen != 0 {
			continue
		}

		unit := runes[:unitLen]
		matches := true

		for i := unitLen; i < len(runes); i += unitLen {
			if string(runes[i:i+unitLen]) != string(unit) {
				matches = false
				break
			}
		}

		if matches {
			return true
		}
	}

	return false
}

// keyboardRows are checked alongside codepoint runs, because "qwertyuiopasdfgh"
// is trivially guessable but is not sequential in any character encoding.
var keyboardRows = []string{
	"qwertyuiop",
	"asdfghjkl",
	"zxcvbnm",
	"1234567890",
	"azertyuiop",
	"qwertzuiop",
}

// isSequential catches ascending or descending runs -- "123456789012345",
// "abcdefghijklmno" -- and straight lines across the keyboard.
func isSequential(candidate string) bool {
	runes := []rune(candidate)
	const minSequenceLen = 2

	if len(runes) < minSequenceLen {
		return false
	}

	ascending, descending := true, true

	for i := 1; i < len(runes); i++ {
		if runes[i] != runes[i-1]+1 {
			ascending = false
		}

		if runes[i] != runes[i-1]-1 {
			descending = false
		}
	}

	if ascending || descending {
		return true
	}

	// A password that is MOSTLY one keyboard row is no better than one that is
	// entirely that row. The run is measured as a fraction of the password rather
	// than as a flat length, because merely containing a short run is not
	// disqualifying: "password12345678" is weak because of "password", and
	// reporting it as "sequential" would send the user off fixing the wrong half.
	const (
		minKeyboardRun      = 8
		keyboardRunFraction = 0.6
	)

	longest := longestKeyboardRun(candidate)

	return longest >= minKeyboardRun && float64(longest) >= keyboardRunFraction*float64(len(runes))
}

// longestKeyboardRun returns the length of the longest contiguous slice of any
// keyboard row (in either direction) that appears in the password.
func longestKeyboardRun(candidate string) int {
	longest := 0

	for _, row := range keyboardRows {
		for _, direction := range []string{row, reverse(row)} {
			for start := range len(direction) {
				for end := start + 1; end <= len(direction); end++ {
					run := direction[start:end]
					if len(run) > longest && strings.Contains(candidate, run) {
						longest = len(run)
					}
				}
			}
		}
	}

	return longest
}

// containsIdentifier rejects a password built around the user's own username or
// the service name. Short identifiers are ignored: refusing every password
// containing a two-letter local part would reject far too much.
func containsIdentifier(candidate, identifier string) bool {
	const minIdentifierLen = 4

	identifier = strings.ToLower(strings.TrimSpace(identifier))
	if utf8.RuneCountInString(identifier) < minIdentifierLen {
		return false
	}

	return strings.Contains(candidate, identifier)
}

func reverse(s string) string {
	runes := []rune(s)
	for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
		runes[i], runes[j] = runes[j], runes[i]
	}

	return string(runes)
}
