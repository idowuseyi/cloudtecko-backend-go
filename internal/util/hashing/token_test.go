package hashing_test

import (
	"encoding/hex"
	"testing"

	"github.com/google/uuid"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util/hashing"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewTokenReturnsUUIDv4AndItsHash(t *testing.T) {
	t.Parallel()

	plaintext, hash, err := hashing.NewToken()
	require.NoError(t, err)

	// The plaintext must stay a UUIDv4: the auth middleware format-validates it
	// before touching the database, and the public API contract says so.
	parsed, err := uuid.Parse(plaintext)
	require.NoError(t, err, "token must be a parseable UUID")
	assert.Equal(t, uuid.Version(4), parsed.Version())

	assert.Len(t, hash, 32, "SHA-256 digest")
	assert.Equal(t, hashing.HashToken(plaintext), hash)

	// The whole point: the stored value must not contain the plaintext.
	assert.NotContains(t, hex.EncodeToString(hash), plaintext)
}

func TestNewTokenIsUnique(t *testing.T) {
	t.Parallel()

	const iterations = 1000

	seen := make(map[string]struct{}, iterations)

	for range iterations {
		plaintext, _, err := hashing.NewToken()
		require.NoError(t, err)

		_, duplicate := seen[plaintext]
		require.False(t, duplicate, "NewToken produced a duplicate, which would mean it is not reading from a CSPRNG")

		seen[plaintext] = struct{}{}
	}
}

func TestHashTokenIsDeterministicAndDistinguishing(t *testing.T) {
	t.Parallel()

	first := hashing.HashToken("a-token")
	second := hashing.HashToken("a-token")

	assert.Equal(t, first, second, "hashing must be deterministic")
	assert.NotEqual(t, first, hashing.HashToken("b-token"))

	// A single-character difference must not survive into the stored value.
	assert.NotEqual(t,
		hashing.HashToken("1cfc27d7-a178-4051-802b-f3ff3967c95c"),
		hashing.HashToken("1cfc27d7-a178-4051-802b-f3ff3967c95d"),
	)
}

func TestTokenHashEqual(t *testing.T) {
	t.Parallel()

	hash := hashing.HashToken("a-token")

	assert.True(t, hashing.TokenHashEqual(hash, hashing.HashToken("a-token")))
	assert.False(t, hashing.TokenHashEqual(hash, hashing.HashToken("another-token")))
	assert.False(t, hashing.TokenHashEqual(hash, nil))
	assert.False(t, hashing.TokenHashEqual(hash, hash[:16]), "differing lengths must not compare equal")
}
