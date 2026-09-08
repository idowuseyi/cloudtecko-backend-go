# Running locally

Two supported ways to run this service. They differ only in where the app
process lives; both use the same binary and the same configuration.

| | App runs on | Database |
| --- | --- | --- |
| **Without Docker** | your host | a managed Postgres, or the container one |
| **With Docker** | the dev container | the compose Postgres |

## Without Docker

### Prerequisites

- **Go 1.27+** (`go.mod` requires 1.27.0). Nothing else is needed to *build*:
  all generated code — `internal/models/`, `internal/types/`, `api/swagger.yml`,
  `internal/api/handlers/handlers.go`, `internal/api/wire_gen.go` — is committed,
  so `go build` works on a fresh checkout.
- A **PostgreSQL** database. Either a managed one (Neon, Aiven, Supabase,
  Render, RDS) or the compose container via `make dev-infra-up`.
- `psql` if you want to inspect the database by hand.

You do **not** need go-swagger, sqlboiler, sql-migrate, pg_format, spectral or
yq unless you are regenerating code. See [Regenerating code](#regenerating-code).

### Configure

```sh
cp .env.local.sample .env.local
```

Then set at minimum:

```sh
DATABASE_URL=postgres://user:password@host:5432/dbname?sslmode=require
SERVER_MANAGEMENT_SECRET=some-long-random-string
```

`.env.local` is gitignored. Two things about it are worth knowing:

- It **overrides** real environment variables (it is applied with `os.Setenv`),
  which is why it must never hold anything you would not want to win.
- For exactly that reason, `docker-compose.yml` sets `DOTENV_DISABLE=true`.
  Without that guard, putting cloud credentials here would silently point the
  *containerised* app at your cloud database — including `server --migrate`.

### Run

```sh
make dev-infra-up     # optional: postgres + integresql + mailhog in Docker
make run-migrate      # apply migrations
make run              # start the server on :8080
```

`make run-env` prints the fully resolved configuration (secrets elided) and is
the fastest way to check what the app actually thinks its settings are.

### Why `PROJECT_ROOT_DIR` matters

`util.GetProjectRootDir()` defaults to the directory of the executable. Since
`make go-build` writes to `./bin/app`, that resolves to `<repo>/bin`, and the app
then cannot find `migrations/`, `web/templates/`, `web/i18n/` or
`api/swagger.yml`, failing at startup.

The `make run*` targets export it for you. Invoking the binary directly does not:

```sh
PROJECT_ROOT_DIR=$PWD ./bin/app server
```

### Connection URLs by provider

`DATABASE_URL` takes precedence over the discrete `PG*` variables. When it
carries no `sslmode`, `require` is assumed — a URL essentially always points at
a remote database.

Query parameters the lib/pq driver cannot handle are dropped, with a warning
naming them. This is deliberate: lib/pq forwards options it does not recognise
to the server, which rejects them as *unrecognized configuration parameter*. 
Neon's own connection string includes `channel_binding=require`, so pasting it
verbatim would otherwise fail at connect time with a confusing server-side error.

| Provider | Notes |
| --- | --- |
| **Neon** | Use the pooled URL for `DATABASE_URL`. `channel_binding` is dropped automatically. `DATABASE_URL_UNPOOLED` is picked up for migrations without further configuration. |
| **Aiven** | Requires `sslmode=verify-full` and a CA bundle: `?sslmode=verify-full&sslrootcert=/path/ca.pem`. Both are honoured. |
| **Supabase** | Port 6543 is the transaction pooler, 5432 is direct. Use 6543 for `DATABASE_URL` and 5432 for `MIGRATION_DATABASE_URL`. |
| **Render / RDS** | Plain `postgres://` URL; keep `sslmode=require`. |

Set `MIGRATION_DATABASE_URL` to the **direct**, non-pooled endpoint whenever
`DATABASE_URL` points at a pooler — transaction poolers cannot run DDL reliably.

## With Docker

Unchanged from the template:

```sh
./docker-helper.sh --up     # starts the stack and drops you into the container
make build                  # full build incl. code generation
make test
```

Inside the container everything is at `/app`, and the compose environment
supplies `PG*` directly.

## Tests

```sh
make dev-infra-up
make test
```

**32 of 47 packages run on the host.** The other 15 need a live **IntegreSQL**
server, and they fail rather than skip without it — `internal/test/test_database.go`
panics in `init()` if the client cannot be constructed.

IntegreSQL is not reachable from the host even when the container is running: it
hands back connection details naming the compose-internal hostname `postgres`,
which does not resolve outside the Docker network. **Run the database-backed
tests inside the dev container**, which is what CI does.

`internal/models` additionally needs the sqlboiler `PSQL_*` spec database
(`make sql-spec-reset && make sql-spec-migrate`).

## Regenerating code

Only needed when you change an input. All of these want the dev container,
since that is where the tooling is pinned:

| You changed | Run | Needs |
| --- | --- | --- |
| `migrations/*.sql` | `make sql` | psql, sql-migrate, sqlboiler, pg_format, a live spec DB |
| `api/paths/*`, `api/definitions/*` | `make swagger` | go-swagger, yq, spectral, rsync |
| added/removed a handler | `make go-generate-handlers` | Go only |
| DI wiring | `make go-generate` | wire (`go install tool`) |

CI enforces that committed generated output matches: it runs `make build` and
then `git diff --exit-code`.

## Useful targets

| Target | Does |
| --- | --- |
| `make run` | build and start the server on the host |
| `make run-migrate` | apply migrations from the host |
| `make run-env` | print the resolved config (secrets elided) |
| `make dev-infra-up` / `-down` | start/stop only postgres, integresql, mailhog |
| `make lint` | golangci-lint plus the generated-code checks |
| `make test` | full suite via gotestsum |
| `make govulncheck` | Go vulnerability database scan |
| `make trivy` | fail on HIGH/CRITICAL findings |
| `make help` | everything else |
