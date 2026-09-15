// Package secretbox encrypts values that must be recoverable.
//
// Almost every credential in this codebase is hashed: access tokens, refresh
// tokens, password-reset and confirmation tokens, invitation tokens, and the
// donor and order access tokens. Hashing is always preferable, because a hash
// cannot be turned back into the secret no matter who reads the database.
//
// This package exists for the one case where that is impossible. Paystack
// returns a subscription's email_token exactly once, at creation, and requires
// it BACK -- alongside the subscription code -- to cancel that subscription. A
// one-way hash would mean a donor can never stop giving through our own
// interface. So it is encrypted rather than hashed, and the trade-off is
// deliberate and narrow: this is the only thing that should ever use it.
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

var (
	// ErrKeyMissing means no encryption key is configured. Callers must treat
	// this as fatal at startup rather than degrading to plaintext: silently
	// storing a live payment credential unencrypted is worse than refusing to
	// boot.
	ErrKeyMissing = errors.New("secretbox: no encryption key configured")

	// ErrDecryptFailed covers a wrong key, a truncated value and a tampered
	// one alike. They are not distinguished on purpose -- telling a caller which
	// of those happened is a decryption oracle.
	ErrDecryptFailed = errors.New("secretbox: failed to decrypt")
)

// Box encrypts and decrypts with AES-256-GCM.
//
// GCM is authenticated: a tampered ciphertext fails to open rather than
// decrypting to garbage, so a value that comes back out is one we put in.
type Box struct {
	aead cipher.AEAD
}

// New derives a key from the configured secret.
//
// SHA-256 of the secret is used as the key so any length of configured string
// works, rather than forcing operators to supply exactly 32 bytes and getting a
// truncated or rejected key when they do not. This is not password hashing --
// the input is a high-entropy configured secret, not something memorised -- so
// a fast hash is the right tool and Argon2 would only add startup cost.
func New(secret string) (*Box, error) {
	if secret == "" {
		return nil, ErrKeyMissing
	}

	key := sha256.Sum256([]byte(secret))

	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, fmt.Errorf("secretbox: failed to create cipher: %w", err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("secretbox: failed to create GCM: %w", err)
	}

	return &Box{aead: aead}, nil
}

// Seal encrypts plaintext, returning a base64 string safe for a text column.
//
// A fresh random nonce is generated per call and prefixed to the ciphertext.
// Reusing a nonce under the same key is catastrophic for GCM -- it leaks the
// XOR of two plaintexts and breaks authentication entirely -- so the nonce is
// never derived from anything, and never reused.
func (b *Box) Seal(plaintext string) (string, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("secretbox: failed to generate nonce: %w", err)
	}

	sealed := b.aead.Seal(nonce, nonce, []byte(plaintext), nil)

	return base64.StdEncoding.EncodeToString(sealed), nil
}

// Open decrypts a value produced by Seal.
func (b *Box) Open(encoded string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", ErrDecryptFailed
	}

	nonceSize := b.aead.NonceSize()
	if len(raw) < nonceSize {
		return "", ErrDecryptFailed
	}

	plaintext, err := b.aead.Open(nil, raw[:nonceSize], raw[nonceSize:], nil)
	if err != nil {
		// Deliberately not wrapping err: the underlying message distinguishes
		// authentication failure from other problems, which is exactly the
		// distinction an attacker probing with modified ciphertexts wants.
		return "", ErrDecryptFailed
	}

	return string(plaintext), nil
}
