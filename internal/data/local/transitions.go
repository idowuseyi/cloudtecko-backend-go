package local

import (
	"context"
	"fmt"
	"strings"

	"github.com/aarondl/sqlboiler/v4/boil"
	"github.com/aarondl/sqlboiler/v4/queries"
)

// transitionTable names a table whose status column may be transitioned.
//
// A closed set rather than a free string: the value is interpolated into SQL,
// and a table name cannot be a bind parameter. Keeping the possibilities to
// these constants means no caller can route user input into that position.
type transitionTable string

const (
	transitionTablePayments transitionTable = "payments"
	transitionTableOrders   transitionTable = "orders"
)

// TransitionTableNamesForTest exposes the literals so a test can assert they
// still match the generated model names. models.TableNames is a var and cannot
// initialise a const, so the link is checked by a test rather than by the
// compiler -- a renamed table then fails in CI rather than at runtime, mid-payment.
func TransitionTableNamesForTest() map[string]string {
	return map[string]string{
		"payments": string(transitionTablePayments),
		"orders":   string(transitionTableOrders),
	}
}

// transitionStatus performs a GUARDED state change and reports whether it won.
//
// This is the concurrency primitive the whole commerce surface rests on. The
// predicate and the write are ONE statement, so two callers racing on the same
// row cannot both observe the old state and both act:
//
//	UPDATE <table> SET status = $to WHERE id = $id AND status = ANY($from)
//
// Under READ COMMITTED -- which db.WithTransaction yields, and which commerce
// code must not raise -- the second writer blocks on the first's row lock, then
// re-evaluates the WHERE clause against the newly committed row. If the status
// has moved on, it matches nothing and reports 0 rows affected. That 0 is not an
// error: it is how a caller learns that someone else already did the work, which
// is exactly what makes webhook processing idempotent.
//
// Returns true if THIS call performed the transition.
func transitionStatus(
	ctx context.Context,
	exec boil.ContextExecutor,
	table transitionTable,
	rowID string,
	from []string,
	to string,
) (bool, error) {
	//nolint:gosec // table is a transitionTable constant, never caller input; a
	// table name cannot be a bind parameter.
	statement := fmt.Sprintf(
		`UPDATE %s SET status = $1, updated_at = now() WHERE id = $2 AND status = ANY($3)`,
		string(table),
	)

	result, err := queries.Raw(statement, to, rowID, pgTextArray(from)).ExecContext(ctx, exec)
	if err != nil {
		return false, fmt.Errorf("failed to transition %s to %s: %w", table, to, err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("failed to read rows affected: %w", err)
	}

	return affected == 1, nil
}

// claimHoldRelease takes the exclusive right to give back a payment's stock or
// seat reservation.
//
// Exactly one actor may release a given hold: either lazy expiry, or the webhook
// that reports the payment succeeded. Whoever wins this conditional update is
// the one that decrements the counter; the loser must not touch it.
//
// Without this, the late-webhook case corrupts the counters. Expiry releases the
// hold, then the webhook arrives, decrements a reservation that no longer
// exists, drives quantity_reserved negative, violates its CHECK, aborts the
// transaction and returns non-2xx -- so the money is captured, nothing is
// recorded, and the provider retries for three days.
//
// Returns true if THIS call now owns the release.
func claimHoldRelease(ctx context.Context, exec boil.ContextExecutor, paymentID string) (bool, error) {
	result, err := queries.Raw(
		`UPDATE payments SET hold_released_at = now(), updated_at = now()
		  WHERE id = $1 AND hold_released_at IS NULL`,
		paymentID,
	).ExecContext(ctx, exec)
	if err != nil {
		return false, fmt.Errorf("failed to claim hold release: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("failed to read rows affected: %w", err)
	}

	return affected == 1, nil
}

// pgTextArray renders a Go slice as a PostgreSQL text[] literal for = ANY(...).
//
// lib/pq cannot bind a []string directly, and building the list with placeholders
// would collide with the positional parameters already in use.
func pgTextArray(values []string) string {
	quoted := make([]string, len(values))
	for i, value := range values {
		quoted[i] = `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value) + `"`
	}

	return "{" + strings.Join(quoted, ",") + "}"
}
