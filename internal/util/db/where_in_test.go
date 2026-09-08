package db_test

import (
	"testing"

	"github.com/idowuseyi/cloudtecko-backend-go/internal/models"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/test"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util/db"
	"github.com/aarondl/sqlboiler/v4/queries"
	"github.com/aarondl/sqlboiler/v4/queries/qm"
)

func TestWhereIn(t *testing.T) {
	query := models.NewQuery(
		qm.Select("*"),
		qm.From("users"),
		db.InnerJoin("users", "id", "app_user_profiles", "user_id"),
		db.WhereIn("app_user_profiles", "username", []string{"max", "muster", "peter"}),
	)

	sql, args := queries.BuildQuery(query)

	test.Snapshoter.Label("SQL").Save(t, sql)
	test.Snapshoter.Label("Args").Save(t, args...)
}

func TestNIN(t *testing.T) {
	query := models.NewQuery(
		qm.Select("*"),
		qm.From("users"),
		db.InnerJoin("users", "id", "app_user_profiles", "user_id"),
		db.NIN("app_user_profiles.username", []string{"max", "muster", "peter"}),
	)

	sql, args := queries.BuildQuery(query)

	test.Snapshoter.Label("SQL").Save(t, sql)
	test.Snapshoter.Label("Args").Save(t, args...)
}
