package db_test

import (
	"errors"
	"testing"

	fgerrors "github.com/friendsofgo/errors"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util/db"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
)

// A referential-integrity refusal arrives under two different SQLSTATEs
// depending on the FK's action: NO ACTION gives 23503, RESTRICT gives 23001.
// The message is identical for both, so only the code distinguishes them --
// and this schema uses RESTRICT on the category FKs.
func TestIsForeignKeyViolationCoversRestrictAndNoAction(t *testing.T) {
	t.Parallel()

	noAction := &pq.Error{Code: "23503", Constraint: "events_category_id_fkey"}
	restrict := &pq.Error{Code: "23001", Constraint: "events_category_id_fkey"}

	assert.True(t, db.IsForeignKeyViolation(noAction, ""), "23503 foreign_key_violation")
	assert.True(t, db.IsForeignKeyViolation(restrict, ""), "23001 restrict_violation")

	// sqlboiler wraps every driver error twice (withStack -> withMessage), so
	// detection must survive unwrapping.
	assert.True(t, db.IsForeignKeyViolation(
		fgerrors.Wrap(restrict, "models: unable to delete from categories"), ""),
		"still detected through sqlboiler's wrapping")

	assert.True(t, db.IsForeignKeyViolation(restrict, "events_category_id_fkey"))
	assert.False(t, db.IsForeignKeyViolation(restrict, "some_other_fkey"),
		"a named constraint must actually match")
}

func TestIsUniqueViolation(t *testing.T) {
	t.Parallel()

	err := &pq.Error{Code: "23505", Constraint: "events_slug_key"}

	assert.True(t, db.IsUniqueViolation(err, ""))
	assert.True(t, db.IsUniqueViolation(err, "events_slug_key"))
	// A table can carry several unique indexes; conflating them would report the
	// wrong conflict to the client.
	assert.False(t, db.IsUniqueViolation(err, "events_pkey"))
	assert.False(t, db.IsUniqueViolation(&pq.Error{Code: "23503"}, ""))
}

func TestIsCheckViolation(t *testing.T) {
	t.Parallel()

	err := fgerrors.Wrap(&pq.Error{Code: "23514", Constraint: "events_time_order_check"}, "models: unable to insert")

	assert.True(t, db.IsCheckViolation(err, "events_time_order_check"))
	assert.False(t, db.IsCheckViolation(err, "orders_total_sum_check"))
}

func TestNonPostgresErrorsAreNotMisclassified(t *testing.T) {
	t.Parallel()

	plain := errors.New("something else went wrong")

	assert.False(t, db.IsUniqueViolation(plain, ""))
	assert.False(t, db.IsForeignKeyViolation(plain, ""))
	assert.False(t, db.IsCheckViolation(plain, ""))
	assert.False(t, db.IsUniqueViolation(nil, ""))
}
