package local

import (
	"crypto/rand"
	"encoding/base32"
	"fmt"
)

// referenceRandomBytes gives 80 bits of entropy, rendered as 16 base32
// characters.
//
// References are unguessable on purpose. They are printed on receipts, travel in
// callback URLs, and are the key a provider uses to identify a transaction -- so
// a sequential or predictable reference would let anyone enumerate other
// people's payments through any endpoint that accepts one.
const referenceRandomBytes = 10

// referenceEncoding is unpadded, uppercase base32 without the confusable
// characters, so a reference read aloud or copied from a printed receipt does
// not turn into a different one.
// The alphabet is RFC 4648 base32, which is exactly 32 characters and entirely
// alphanumeric -- so no symbol needs substituting afterwards.
//
// The previous alphabet dropped the confusable I, L and O and padded the set to
// 32 with "!", then rewrote "!" to "7" after encoding. That fold made two
// distinct 5-bit values render as the same character, discarding entropy and
// letting two different random draws produce the same reference. Confusability
// does not matter here: a payment reference is machine-handled, never read
// aloud. Ticket codes, which are, keep their own restricted alphabet.
var referenceEncoding = base32.NewEncoding("ABCDEFGHIJKLMNOPQRSTUVWXYZ234567").WithPadding(base32.NoPadding)

// newPaymentReference returns a prefixed, unguessable reference.
//
// The prefix is operational, not security: it makes a reference recognisable in
// a provider dashboard ("cld_don_..." is clearly one of ours, and clearly a
// donation) without revealing anything an attacker could use.
func newPaymentReference(kind string) (string, error) {
	buf := make([]byte, referenceRandomBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("failed to generate payment reference: %w", err)
	}

	return "cld_" + kind + "_" + referenceEncoding.EncodeToString(buf), nil
}

// ReferenceAlphabetForTest renders all 32 base32 values so a test can assert the
// alphabet needs no post-encoding substitution.
//
// An exported seam rather than an in-package test, matching
// TransitionTableNamesForTest: the linter requires _test packages, and the
// property being checked is about the encoding, not about any caller.
func ReferenceAlphabetForTest() string {
	// Probe bytes chosen so the 5-bit groups walk 0..31 exactly once.
	return referenceEncoding.EncodeToString([]byte{
		0x00, 0x44, 0x32, 0x14, 0xc7, 0x42, 0x54, 0xb6, 0x35, 0xcf,
		0x84, 0x65, 0x3a, 0x56, 0xd7, 0xc6, 0x75, 0xbe, 0x77, 0xdf,
	})
}

// NewPaymentReferenceForTest exposes reference minting to a _test package.
func NewPaymentReferenceForTest(kind string) (string, error) {
	return newPaymentReference(kind)
}
