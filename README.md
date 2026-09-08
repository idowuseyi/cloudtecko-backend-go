# cloudtecko-backend-go

Cloudtecko's backend API: a stateless RESTful JSON service in Go.

Echo v4 · PostgreSQL via sqlboiler · google/wire DI · argon2id auth ·
sql-migrate · OpenAPI 2.0

Bootstrapped from [allaboutapps/go-starter](https://github.com/allaboutapps/go-starter)
and since adapted; upstream documentation still applies to the parts that have
not been changed.

## Quick start

Without Docker (needs Go 1.27+ and a PostgreSQL database):

```sh
cp .env.local.sample .env.local   # set DATABASE_URL and SERVER_MANAGEMENT_SECRET
make dev-infra-up                 # optional: postgres + integresql + mailhog in Docker
make run-migrate
make run                          # http://localhost:8080
```

With Docker:

```sh
./docker-helper.sh --up
make build && make test
```

All generated code is committed, so `go build` works on a fresh checkout — no
code generation step is required to run the service.

See [docs/running-locally.md](docs/running-locally.md) for the detail, including
per-provider database URLs and which tests need which infrastructure.

## Documentation

| | |
| --- | --- |
| [Running locally](docs/running-locally.md) | both workflows, configuration, tests, code generation |
| [Deployment](docs/deployment.md) | Render and Vercel, environment matrix |
| [Database dialects](docs/database-dialects.md) | why Postgres only, and what changing that would cost |
| [Security audit](docs/security/2026-09-08-security-audit.md) | findings, what was fixed, what is deferred |
| [Migration guides](docs/migration-guides/) | inherited from upstream |

## Configuration

Everything is environment-driven. `make run-env` prints the fully resolved
configuration with secrets elided — the fastest way to see what the app actually
believes its settings are.

`DATABASE_URL` (the form every managed provider hands out) takes precedence over
the discrete `PG*` variables; `PG*` is what docker-compose supplies. Set
`MIGRATION_DATABASE_URL` to a direct, non-pooled endpoint when `DATABASE_URL`
points at a connection pooler.

`.env.local` overrides real environment variables, so `docker-compose.yml` sets
`DOTENV_DISABLE=true`. Without that, cloud credentials placed there for the
no-Docker workflow would silently redirect the containerised app — including
`server --migrate` — at the cloud database.

## Endpoints

| Path | Auth |
| --- | --- |
| `/api/v1/auth/*` | bearer token; login, register, refresh and forgot-password are open (and rate limited) |
| `/api/v1/push/*` | bearer token |
| `/-/ready` | none — this is the platform health-check path |
| `/-/healthy`, `/-/version`, `/-/metrics` | `?mgmt-secret=` |
| `/swagger.yml` | open, and disabled in production |
| `/.well-known/*` | open |

## Project layout

```
api/          OpenAPI spec sources; swagger.yml is generated from them
cmd/          cobra commands: server, db migrate|seed, env, probe
internal/
  api/        echo server, router, middleware, handlers
  auth/       registration, login, tokens, password reset
  config/     all environment parsing
  models/     sqlboiler-generated (do not edit)
  persistence/ the SQL dialect seam
  types/      go-swagger-generated request/response types (do not edit)
  util/db/    query helpers (Postgres-specific)
migrations/   sql-migrate DDL
scripts/      gsdev developer tooling, behind the `scripts` build tag
web/          email + view templates, i18n bundles
.workflow/    ship project-lifecycle trail
```

## Security

Report vulnerabilities per [SECURITY.md](SECURITY.md). Do not open public issues.

Known deferred items — token storage and refresh-token lifetime — are recorded
in the [security audit](docs/security/2026-09-08-security-audit.md) with the
conditions under which they must be addressed.

## Licence

MIT. See [LICENSE](LICENSE); the upstream go-starter copyright notice is
retained as the licence requires.
