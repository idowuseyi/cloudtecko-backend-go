package password

import (
	"strings"
	"sync"
	"unicode"
)

// The blocklist is shaped by the 15-character minimum.
//
// Most entries in a breach corpus are shorter than 15 characters and are already
// excluded by length, so a general "top 10,000 passwords" list would be almost
// entirely dead weight here. What actually gets past a length rule is a common
// word padded to reach it -- "password12345678", "welcometocloudtecko" -- or a
// memorable phrase. That is what this targets.
//
// This is NOT a substitute for a breach-corpus check against a service such as
// HaveIBeenPwned's k-anonymity range API, which would catch passwords that are
// individually unusual but known to be compromised. That is deliberately not
// wired in here: it would put an outbound network call on the registration path,
// and the failure policy (reject or allow when the service is unreachable) is a
// product decision, not a technical one. See docs/security/.

// commonPasswords are known weak values that survive a 15-character minimum,
// stored lowercased. Built once on first use rather than in an init(), so the
// cost is not paid by programs that never validate a password.
var commonPasswords = sync.OnceValue(buildCommonPasswords)

// commonBases are words that, alone or padded with digits and punctuation to
// reach the minimum length, do not make a password.
var commonBases = []string{
	"password", "passwort", "contrasena", "motdepasse",
	"welcome", "letmein", "iloveyou", "trustno1", "changeme", "secret",
	"admin", "administrator", "root", "superuser", "guest", "default",
	"qwerty", "azerty", "monkey", "dragon", "football", "baseball",
	"sunshine", "princess", "shadow", "master", "michael", "jennifer",
	"login", "test", "temp", "temporary", "example", "sample", "demo",
	"cloudtecko", "backend", "server", "database", "postgres",
	"summer", "winter", "spring", "autumn", "january", "december",
	"whatever", "nothing", "anything", "something", "mypassword",
	"correcthorsebatterystaple", // the xkcd example, now itself common
}

func buildCommonPasswords() map[string]struct{} {
	blocked := make(map[string]struct{})

	// Values that are exactly a common base repeated or padded are generated
	// rather than typed out, so the list stays readable and stays in sync.
	for _, base := range commonBases {
		for _, variant := range []string{
			base + base,
			base + base + base,
			base + "123456789",
			base + "1234567890",
			base + "12345678",
		} {
			blocked[variant] = struct{}{}
		}
	}

	for _, extra := range []string{
		"passwordpassword", "password1234567", "p@ssw0rdp@ssw0rd",
		// Keyboard COLUMN walks, which the row check cannot see.
		"1qaz2wsx3edc4rfv", "zaq12wsxcde34rfv",
		"thisismypassword", "letmeinplease123", "iloveyouforever1",
		"administrator123", "supersecretpass1", "changemeplease12",
		"nevergonnagiveyouup",
	} {
		blocked[extra] = struct{}{}
	}

	return blocked
}

// isCommon reports whether a (already lowercased and normalized) password is a
// known weak value, or is merely a common word padded out to reach the minimum
// length.
func isCommon(candidate string) bool {
	if _, found := commonPasswords()[candidate]; found {
		return true
	}

	// "password12345678" and "1234password!!!!" are the same idea as "password".
	// Strip the non-letter padding and see whether what is left is just a common
	// word, so the length requirement cannot be satisfied with filler alone.
	stripped := strings.TrimFunc(candidate, func(r rune) bool {
		return !unicode.IsLetter(r)
	})

	if stripped == "" {
		return false
	}

	for _, base := range commonBases {
		if stripped == base {
			return true
		}
	}

	return false
}
