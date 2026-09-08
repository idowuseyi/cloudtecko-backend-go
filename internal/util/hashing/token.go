package hashing

import (
	"crypto/sha256"
	"crypto/subtle"
	"fmt"

	"github.com/google/uuid"
)

// Session tokens (access and refresh) are stored as a hash, never as the value
// handed to the client, so read access to the database is not equivalent to
// holding every live session.
//
// SHA-256 is deliberate, and is NOT the same decision as password hashing:
//
//   - A token is 122 bits of CSPRNG output, so there is no guessable input to
//     brute force. A password is low-entropy and human-chosen, which is why it
//     needs Argon2's deliberate slowness.
//   - The token hash is computed on EVERY authenticated request. Argon2 here
//     would add tens of milliseconds and ~64MB of allocation per request, which
//     is a denial-of-service surface, not a security gain.
//
// The plaintext stays a UUIDv4 string so the format validator in
// internal/api/middleware/auth.go and the public API contract are unchanged.

// NewToken returns a fresh token: the plaintext to hand to the client, and the
// hash to persist. The plaintext is never stored.
func NewToken() (string, []byte, error) {
	// uuid.NewRandom reads from crypto/rand and reports failure rather than
	// silently returning a predictable value, unlike uuid.New which panics.
	id, err := uuid.NewRandom()
	if err != nil {
		return "", nil, fmt.Errorf("failed to generate token: %w", err)
	}

	plaintext := id.String()

	return plaintext, HashToken(plaintext), nil
}

// HashToken returns the stored representation of a token.
func HashToken(plaintext string) []byte {
	sum := sha256.Sum256([]byte(plaintext))

	return sum[:]
}

// TokenHashEqual compares two token hashes in constant time.
//
// Lookups are by primary key, so the database has already leaked timing through
// the index; this exists for the paths that compare a hash in memory, where the
// cost of being careful is nil.
func TokenHashEqual(a, b []byte) bool {
	return subtle.ConstantTimeCompare(a, b) == 1
}
