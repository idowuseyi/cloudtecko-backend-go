# Database dialects

**Only PostgreSQL is supported.** This document exists so that stays a decision
rather than an accident, and so the cost of changing it is known rather than
guessed at.

## What the seam does, and what it does not

`internal/persistence` dispatches on `config.Database.Dialect`:

| File | Role |
| --- | --- |
| `persistence.go` | `NewDB` dispatch, `ErrUnsupportedDialect`, shared pool settings |
| `postgres.go` | the lib/pq implementation |
| `mysql.go` | rejects with an explanation |

`DB_DIALECT` selects it, and a `mysql://` `DATABASE_URL` is parsed rather than
rejected, purely so the failure arrives with a useful message instead of a URL
parse error.

The seam makes the *choice* explicit. It does **not** make MySQL nearly work.
Opening a MySQL connection would still leave every query in the application
broken, which is why `mysql.go` refuses rather than connecting.

## What supporting MySQL would actually require

The coupling is deep but finite. This is the complete list.

### 1. The generated data layer — `internal/models/`

25 files of sqlboiler output generated with the `psql` driver.

- `boil_queries.go` pins the dialect: double-quoted identifiers, `$1`-style
  index placeholders, no `LastInsertId`. MySQL needs backticks, `?` placeholders
  and `UseLastInsertID: true`.
- `psql_upsert.go` emits `INSERT ... ON CONFLICT (...) DO UPDATE SET ...
  EXCLUDED.x` plus `RETURNING`. MySQL has neither; it needs
  `ON DUPLICATE KEY UPDATE` and a follow-up `SELECT`, because it cannot return
  the inserted row.
- `RETURNING` appears 8 times, all inside `internal/models/`.

Regenerating with `sqlboiler mysql` produces a different API surface in places
(no `RETURNING` means the generated `Insert` cannot populate defaults), so call
sites change too.

### 2. The schema — `migrations/`

All ten migrations are Postgres DDL:

| Construct | Where | MySQL equivalent |
| --- | --- | --- |
| `CREATE EXTENSION "uuid-ossp"` | `20200428064736` | none — drop it; generate UUIDs in Go |
| `uuid` columns with `DEFAULT uuid_generate_v4()` | users, all token tables | `BINARY(16)`/`CHAR(36)`, generated application-side |
| `CREATE TYPE ... AS ENUM` | `20200428064802` (`user_scope`), `20200529112509` (`provider_type`) | inline `ENUM(...)` on the column |
| `scopes text[]` | `20200428072232` | a join table, or JSON |
| `timestamptz` | everywhere | `TIMESTAMP`, with an explicit UTC policy |
| `CREATE SEQUENCE seq_health` | `20200811180414` | `AUTO_INCREMENT` table, or drop the liveness query |

A second migration directory is needed; sql-migrate can select by dialect, but
the files themselves cannot be shared.

### 3. The query helpers — `internal/util/db/`

Six functions are Postgres-only, and they are the whole non-generated coupling:

| Location | Construct |
| --- | --- |
| `ilike.go:26` | `ILIKE` (MySQL's default collation is already case-insensitive) |
| `where_in.go:20` | `= any(?)` with `pq.StringArray` |
| `where_in.go:24` | `<> all(?)` with `pq.StringArray` |
| `json.go:114` | `->` with `<@ to_jsonb(?::text[])` — JSONB containment |
| `json.go:116` | `->>` — JSON text extraction |
| `ts_vector.go:19` | `tsquery` full-text search |

`github.com/lib/pq` is imported directly (not merely as a driver) at exactly
three non-test lines, all in this package.

### 4. Everything else

- `internal/api/handlers/common/probes.go:139` — `SELECT nextval('seq_health')`,
  the only raw SQL on a request path.
- `dbconfig.yml` — both environments declare `dialect: postgres`.
- `internal/models/psql_main_test.go` — the sqlboiler test harness shells out to
  `pg_dump` and `psql`.
- `internal/config/db_url.go` — `libpqSafeParams` and the DSN builder are
  libpq-shaped; MySQL needs a `user:pass@tcp(host:port)/db?params` DSN.
- `Makefile` — `sql-format` (pgFormatter), `sql-check-syntax` (wraps each file in
  a `DO $$ ... $$` block and pipes it to `psql`), `sql-spec-*`, and the
  `scripts/sql/*.sql` structure checks.
- The test suite depends on **IntegreSQL**, which is Postgres-only. There is no
  equivalent for MySQL, so the template-database strategy behind
  `WithTestDatabase` would have to be replaced outright.

## Recommendation

Don't. The realistic paths are:

1. **Stay on Postgres.** Every managed provider offers it, and `DATABASE_URL`
   already makes switching provider a config change.
2. **If a MySQL database must be read** (an existing system, say), add it as a
   *second, separate* connection for that purpose rather than moving the
   application's own schema. The seam would need extending to hand out a named
   connection; the primary data layer stays Postgres.
3. **A genuine port** is a multi-week project touching `internal/models/`,
   `migrations/`, `internal/util/db/` and the entire test strategy. It should be
   scoped as its own piece of work with its own plan.
