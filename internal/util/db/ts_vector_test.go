package db_test

import (
	"testing"

	"github.com/go-openapi/swag/conv"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util/db"
	"github.com/stretchr/testify/assert"
)

func TestSearchStringToTSQuery(t *testing.T) {
	expected := "'abcde':* & '12345':* & 'xyz':*"
	search := conv.Pointer("    abcde 12345 xyz   ")
	out := db.SearchStringToTSQuery(search)
	assert.Equal(t, expected, out)

	expected = "'abcde':*"
	search = conv.Pointer("abcde")
	out = db.SearchStringToTSQuery(search)
	assert.Equal(t, expected, out)

	expected = "'Hello':* & 'world':* & 'lorem':* & '12345':* & 'ipsum':* & 'abc':* & 'def':*"
	search = conv.Pointer("    Hello  world lorem 12345               ipsum  abc def  ")
	out = db.SearchStringToTSQuery(search)
	assert.Equal(t, expected, out)

	expected = ""
	search = nil
	out = db.SearchStringToTSQuery(search)
	assert.Equal(t, expected, out)

	expected = ""
	search = conv.Pointer("")
	out = db.SearchStringToTSQuery(search)
	assert.Equal(t, expected, out)

	expected = ""
	search = conv.Pointer("                ''   '    '    '      ")
	out = db.SearchStringToTSQuery(search)
	assert.Equal(t, expected, out)

	// A trailing backslash used to escape the closing quote of the generated
	// term, producing 'foo\':* -- which to_tsquery rejects with a syntax error.
	// On a public search endpoint that is a 500 anyone can trigger, so every
	// character outside the safe class is now dropped rather than quoted.
	expected = "'foo':*"
	search = conv.Pointer(`foo\`)
	out = db.SearchStringToTSQuery(search)
	assert.Equal(t, expected, out)

	expected = "'foo':*"
	search = conv.Pointer("foo)")
	out = db.SearchStringToTSQuery(search)
	assert.Equal(t, expected, out)

	expected = "'a':* & 'b':*"
	search = conv.Pointer("a|b")
	out = db.SearchStringToTSQuery(search)
	assert.Equal(t, expected, out)

	// Hyphens and underscores are legitimate inside search terms and survive.
	expected = "'multi-word_term':*"
	search = conv.Pointer("multi-word_term")
	out = db.SearchStringToTSQuery(search)
	assert.Equal(t, expected, out)

	// Non-ASCII letters and digits are letters too.
	expected = "'Lagos':* & 'Abuja':*"
	search = conv.Pointer("Lagos, Abuja")
	out = db.SearchStringToTSQuery(search)
	assert.Equal(t, expected, out)

	// Input consisting only of unsafe characters degrades to "no search",
	// never to a malformed query.
	expected = ""
	search = conv.Pointer(`!@#$%^&*()<->\`)
	out = db.SearchStringToTSQuery(search)
	assert.Equal(t, expected, out)
}
