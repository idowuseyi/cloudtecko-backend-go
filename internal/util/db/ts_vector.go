package db

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/aarondl/sqlboiler/v4/queries/qm"
)

var (
	// tsQueryUnsafeRegex matches everything that is NOT a letter, a digit, an
	// underscore, a hyphen or whitespace.
	//
	// Allow-list rather than deny-list, deliberately. The previous approach
	// stripped single quotes and wrapped each term in quotes, which handles most
	// tsquery operators -- '(', '|' and '&' are inert inside a quoted lexeme --
	// but not a trailing backslash, which escapes the closing quote and yields
	// 'foo\':*. to_tsquery rejects that with a syntax error, and since this
	// function feeds a public search endpoint, that is a 500 any visitor can
	// trigger by typing a backslash. Enumerating the dangerous characters is how
	// that was missed once already; enumerating the safe ones cannot be.
	tsQueryUnsafeRegex = regexp.MustCompile(`[^\p{L}\p{N}_\-\s]+`)

	// tsQueryMeaningfulRegex requires a term to carry at least one letter or
	// digit. Hyphens and underscores are kept inside words ("multi-word_term"),
	// but a term made only of them carries no lexeme and would emit '-':* --
	// valid syntax that matches nothing and only adds an AND clause that can
	// never be satisfied.
	tsQueryMeaningfulRegex = regexp.MustCompile(`[\p{L}\p{N}]`)
)

// SearchStringToTSQuery returns a TSQuery string from user input.
// The resulting query will match if every word matches a beginning of a word in the row.
// Characters outside [letters, digits, underscore, hyphen, whitespace] are dropped, and
// the remaining words are combined with AND.
// If no input was given (nil or empty string) or the value contains no word carrying a
// letter or digit, an empty string is returned -- callers treat that as "no search
// filter" rather than as an error, so a query of pure punctuation returns everything
// instead of failing.
func SearchStringToTSQuery(s *string) string {
	if s == nil || len(*s) == 0 {
		return ""
	}

	fields := strings.Fields(tsQueryUnsafeRegex.ReplaceAllString(*s, " "))

	terms := make([]string, 0, len(fields))

	for _, field := range fields {
		if !tsQueryMeaningfulRegex.MatchString(field) {
			continue
		}

		terms = append(terms, "'"+field+"':*")
	}

	return strings.Join(terms, " & ")
}

// TSVectorColumnSearchTSV is the tsvector column on resources.
//
// It is a string literal rather than models.ResourceColumns.SearchTSV because the
// column is GENERATED ALWAYS and is blacklisted in sqlboiler.toml -- without that
// blacklist boil.Infer() includes it in every INSERT and PostgreSQL rejects the
// write. The trade-off is that this one column name is not compile-time checked;
// the resources migration carries a matching note.
const TSVectorColumnSearchTSV = "search_tsv"

// SearchTSVector returns a query mod matching a tsquery against a tsvector column.
// tsQuery must come from SearchStringToTSQuery, which guarantees it is syntactically
// valid; an empty tsQuery yields a no-op mod so callers can pass an unfiltered search
// straight through.
func SearchTSVector(tsQuery string, path ...string) qm.QueryMod {
	if tsQuery == "" {
		return qm.Expr()
	}

	// ! Attention: ? rather than $1, for the same reason ILike documents above --
	// ! other parts of the query may already have claimed positional parameters.
	return qm.Where(fmt.Sprintf("%s @@ to_tsquery('english', ?)", strings.Join(path, ".")), tsQuery)
}

// SearchTSVectorRank orders results by relevance against the same tsquery.
// Pair it with SearchTSVector; the setweight() weighting in the generated column
// makes a title match outrank a body mention by roughly an order of magnitude.
func SearchTSVectorRank(tsQuery string, path ...string) qm.QueryMod {
	if tsQuery == "" {
		return qm.Expr()
	}

	return qm.OrderBy(fmt.Sprintf("ts_rank(%s, to_tsquery('english', ?)) DESC", strings.Join(path, ".")), tsQuery)
}
