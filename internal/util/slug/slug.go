// Package slug builds URL-safe identifiers from human titles.
package slug

import (
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

const (
	// MaxLength bounds a generated slug. Long slugs are hostile in URLs and in
	// share previews, and the disambiguating suffix needs room after truncation.
	MaxLength = 80

	// Fallback is used when a title contains nothing slug-able at all -- a title
	// of only emoji or CJK punctuation, say. Returning "" would produce a URL of
	// /events/ that routes to the list endpoint instead of a detail page.
	Fallback = "item"
)

var (
	nonAlphanumericRegex = regexp.MustCompile(`[^a-z0-9]+`)
	repeatedHyphenRegex  = regexp.MustCompile(`-{2,}`)

	// Reserved slugs would collide with sibling routes on the same path segment.
	// "featured" is the concrete case: /api/v1/events/featured is a real endpoint,
	// so an event slugging to "featured" would be unreachable.
	reserved = map[string]struct{}{
		"admin":    {},
		"api":      {},
		"featured": {},
		"new":      {},
		"search":   {},
	}
)

// Make converts a title into a URL slug: accents folded to ASCII, lowercased,
// runs of non-alphanumerics collapsed to single hyphens, trimmed and truncated.
// A reserved or empty result is suffixed rather than returned as-is.
func Make(title string) string {
	folded, _, err := transform.String(
		transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC),
		title,
	)
	if err != nil {
		// Folding is best-effort: fall back to the raw title rather than failing,
		// since the regex below strips anything unusable anyway.
		folded = title
	}

	slugged := nonAlphanumericRegex.ReplaceAllString(strings.ToLower(folded), "-")
	slugged = repeatedHyphenRegex.ReplaceAllString(slugged, "-")
	slugged = strings.Trim(slugged, "-")

	if len(slugged) > MaxLength {
		slugged = strings.Trim(slugged[:MaxLength], "-")
	}

	if slugged == "" {
		return Fallback
	}

	if _, isReserved := reserved[slugged]; isReserved {
		return slugged + "-1"
	}

	return slugged
}

// NextAvailable returns the first slug in the series base, base-2, base-3, ...
// that is not present in taken.
//
// The caller supplies the taken set from a single prefix query, so allocation is
// one SELECT rather than a retry loop around INSERT. That matters: in PostgreSQL
// a failed INSERT aborts the whole transaction, so "insert, catch 23505, try the
// next suffix" cannot work inside a transaction without a SAVEPOINT per attempt.
// The unique index remains the backstop -- if a concurrent writer takes the slug
// between this call and the INSERT, the constraint fires and the request returns
// 409 rather than silently overwriting.
func NextAvailable(base string, taken map[string]struct{}) string {
	if _, exists := taken[base]; !exists {
		return base
	}

	for i := 2; ; i++ {
		candidate := base + "-" + itoa(i)
		if _, exists := taken[candidate]; !exists {
			return candidate
		}
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}

	var buf [20]byte

	pos := len(buf)

	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}

	return string(buf[pos:])
}
