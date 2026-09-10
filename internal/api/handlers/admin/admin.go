package admin

import (
	"github.com/aarondl/null/v8"
	"github.com/go-openapi/strfmt"
)

// uuidToNullString converts an optional uuid4 body field to the null.String the
// service layer and sqlboiler use. A nil pointer and an empty uuid both mean
// "no media asset"; the empty check matters because go-swagger materialises an
// omitted x-nullable string as a pointer to the zero UUID rather than nil for
// some payload shapes, and writing "" into a uuid FK column is a 22P02 rather
// than the NULL the caller intended.
func uuidToNullString(id *strfmt.UUID4) null.String {
	if id == nil || id.String() == "" {
		return null.String{}
	}

	return null.StringFrom(id.String())
}
