package persistence

import (
	"fmt"

	"github.com/idowuseyi/cloudtecko-backend-go/internal/config"
)

// newMySQLDB is a deliberate placeholder.
//
// MySQL is NOT supported. The seam exists so the decision is explicit and the
// failure is legible, not so that MySQL is "nearly working" -- opening a MySQL
// connection here would still leave every query broken.
//
// Supporting it is a real project, not a config flag. The blocking pieces are:
//
//   - internal/models/ is sqlboiler output generated for the psql driver.
//     boil_queries.go pins the dialect (double-quoted identifiers, $1 placeholders)
//     and psql_upsert.go emits ON CONFLICT ... RETURNING.
//   - migrations/ is Postgres DDL: the uuid-ossp extension, uuid_generate_v4()
//     column defaults, CREATE TYPE ... AS ENUM (user_scope, provider_type), a
//     text[] array column on users.scopes, timestamptz, and CREATE SEQUENCE
//     seq_health.
//   - internal/util/db/ contains six Postgres-only query helpers: ILIKE, any()
//     and all() with pq.StringArray, jsonb containment, the ->> operator, and
//     tsquery full-text search.
//   - internal/api/handlers/common/probes.go issues SELECT nextval('seq_health'),
//     the only raw SQL on a request path.
//   - dbconfig.yml and the sqlboiler test harness shell out to psql and pg_dump.
//
// See docs/database-dialects.md for the full inventory.
func newMySQLDB(cfg config.Database) error {
	return fmt.Errorf(
		"%w: %q is not implemented -- the data layer (internal/models, migrations/, internal/util/db) is Postgres-specific; see docs/database-dialects.md",
		ErrUnsupportedDialect, cfg.Dialect,
	)
}
