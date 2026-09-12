package local

import (
	"crypto/rand"
	"encoding/base32"
	"fmt"
	"strings"
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
var referenceEncoding = base32.NewEncoding("ABCDEFGHJKMNPQRSTUVWXYZ23456789!").WithPadding(base32.NoPadding)

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

	encoded := referenceEncoding.EncodeToString(buf)

	// The alphabet's final character is a placeholder so the set is 32 long;
	// swap any occurrence for a digit to keep references alphanumeric.
	encoded = strings.ReplaceAll(encoded, "!", "7")

	return "cld_" + kind + "_" + encoded, nil
}
