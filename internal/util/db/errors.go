package db

import (
	"errors"

	"github.com/lib/pq"
)

// PostgreSQL error codes we act on by name rather than by string matching.
// https://www.postgresql.org/docs/current/errcodes-appendix.html
const (
	pgErrCodeUniqueViolation = "23505"
	pgErrCodeCheckViolation  = "23514"

	// A referential-integrity refusal arrives under TWO different codes, and
	// which one you get depends on the FK's action rather than on anything the
	// caller did:
	//
	//   ON DELETE NO ACTION (the default) -> 23503 foreign_key_violation
	//   ON DELETE RESTRICT                -> 23001 restrict_violation
	//
	// RESTRICT gets its own code because it is checked immediately and cannot be
	// deferred. The error MESSAGE is identical for both ("violates foreign key
	// constraint"), so matching on text would hide the distinction entirely --
	// which is how a correctly-refused delete first surfaced here as a 500
	// instead of a 409. This schema uses RESTRICT on the category FKs, so both
	// codes must be treated alike.
	pgErrCodeForeignKeyViolation = "23503"
	pgErrCodeRestrictViolation   = "23001"
)

// IsUniqueViolation reports whether err is a PostgreSQL unique-constraint
// violation. When constraint is non-empty it must also match, so a caller can
// distinguish "this slug is taken" from "this SKU is taken" on a table carrying
// several unique indexes -- treating them alike would report the wrong conflict
// to the client.
func IsUniqueViolation(err error, constraint string) bool {
	return isPGError(err, pgErrCodeUniqueViolation, constraint)
}

// IsForeignKeyViolation reports whether err is a PostgreSQL FK violation, which
// for this schema usually means a referenced category or media asset does not
// exist -- a 400 or 404, not a 500.
func IsForeignKeyViolation(err error, constraint string) bool {
	return isPGError(err, pgErrCodeForeignKeyViolation, constraint) ||
		isPGError(err, pgErrCodeRestrictViolation, constraint)
}

// IsCheckViolation reports whether err is a PostgreSQL CHECK violation. This
// schema leans on CHECK constraints for invariants the application must not be
// able to break (order totals, stock counters, time ordering), so hitting one is
// a genuine bug rather than user error and should surface as a 500.
func IsCheckViolation(err error, constraint string) bool {
	return isPGError(err, pgErrCodeCheckViolation, constraint)
}

func isPGError(err error, code string, constraint string) bool {
	var pgErr *pq.Error
	if !errors.As(err, &pgErr) {
		return false
	}

	if string(pgErr.Code) != code {
		return false
	}

	return constraint == "" || pgErr.Constraint == constraint
}
