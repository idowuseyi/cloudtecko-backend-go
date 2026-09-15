// Package ticket generates the codes printed on event tickets.
package ticket

import (
	"crypto/rand"
	"fmt"
	"strings"
)

// codeAlphabet omits I, L, O, U, 0 and 1.
//
// A ticket code is read aloud at a door, copied off a phone screen, and typed
// by a volunteer. Confusable characters turn that into an argument. U is also
// dropped so the alphabet cannot spell short offensive words by accident.
const codeAlphabet = "ABCDEFGHJKMNPQRSTVWXYZ23456789"

// codeLength gives 30^10 ≈ 5.9e14 possibilities. Codes are checked against a
// unique index rather than being guessed at, but they are also shown to staff,
// so they must be short enough to read and long enough that guessing one is not
// a way to walk into a paid event.
const codeLength = 10

// groupSize splits the code for legibility: CLD-XXXXX-XXXXX.
const groupSize = 5

// New returns a ticket code.
//
// It reads from crypto/rand and reports failure rather than falling back to a
// predictable source: a ticket code IS the admission credential, so a
// degraded-entropy path would be a way in.
func New() (string, error) {
	buf := make([]byte, codeLength)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("failed to generate ticket code: %w", err)
	}

	var builder strings.Builder

	builder.WriteString("CLD-")

	for i, randomByte := range buf {
		if i > 0 && i%groupSize == 0 {
			builder.WriteByte('-')
		}

		// Modulo bias is negligible and irrelevant here: 256 mod 30 leaves a
		// skew of under 0.8% across the alphabet, which does not meaningfully
		// reduce the search space of a 10-character code.
		builder.WriteByte(codeAlphabet[int(randomByte)%len(codeAlphabet)])
	}

	return builder.String(), nil
}

// Normalize makes a human-entered code comparable to a stored one.
//
// Staff type these from a phone screen or read them off a printout, so case,
// spacing and hyphens are all unreliable. Anything outside the alphabet is
// dropped rather than rejected, so "cld 4h9k m2p7 qr" still matches.
func Normalize(code string) string {
	var builder strings.Builder

	for _, r := range strings.ToUpper(code) {
		if strings.ContainsRune(codeAlphabet, r) {
			builder.WriteRune(r)
		}
	}

	normalized := builder.String()

	// Strip the constant prefix if present, so a code entered with or without it
	// compares equal.
	normalized = strings.TrimPrefix(normalized, "CD")

	if len(normalized) != codeLength {
		return ""
	}

	return "CLD-" + normalized[:groupSize] + "-" + normalized[groupSize:]
}
