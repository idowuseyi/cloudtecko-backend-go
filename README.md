# cloudtecko-backend-go

The backend API for **Cloudtecko**, a community platform: membership and invitations,
published content and resources, event ticketing, merch orders, one-off and recurring
donations, and the admin surface that runs all of it.

A stateless RESTful JSON service in Go.

**Go · Echo v4 · PostgreSQL (sqlboiler) · google/wire · argon2id · sql-migrate ·
OpenAPI 2.0 · Paystack · S3 · Docker**

- **451 tests** across the suite, run under `-race`, with real PostgreSQL via
  IntegreSQL rather than mocks for anything that touches the database.
- **24 migrations** and an OpenAPI spec that CI checks against the implemented
  handlers, so the spec cannot silently drift from the code.
- Two written security reviews, both with every finding tracked to a status:
  the [September audit](docs/security/2026-09-08-security-audit.md) and the
  [commerce surface review](docs/security/2026-09-15-commerce-surface-review.md).

Bootstrapped from [allaboutapps/go-starter](https://github.com/allaboutapps/go-starter)
and since adapted; upstream documentation still applies to the parts that have not
been changed.

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

All generated code is committed, so `go build` works on a fresh checkout — no code
generation step is required to run the service.

See [docs/running-locally.md](docs/running-locally.md) for the detail, including
per-provider database URLs and which tests need which infrastructure.

## What it does

| Domain | Surface |
| --- | --- |
| **Auth** | register, login, logout, refresh, userinfo, change password, forgot-password and completion |
| **Membership** | applications, review, invitations and invitation acceptance |
| **Content** | published resources, categories, FAQs, contact messages |
| **Events** | listing, featured events, slug lookup, registration, ticketing |
| **Merch** | products and checkout |
| **Donations** | tiers, one-off checkout, recurring giving |
| **Payments** | Paystack behind a provider interface, driven by signature-verified webhooks |
| **Admin** | applications, categories, resources, events, orders, payments, donations, media presigning, webhook-event inspection and an audit log |

## Design notes

The parts worth knowing before changing anything:

- **Payments are provider-shaped, not Paystack-shaped.** `internal/payment` defines
  the interface; `paystack` and `mock` implement it. Webhook handling is
  signature-verified and idempotent, and the same settlement routine serves both the
  webhook and the redirect-verify path, so a payment confirmed twice is recorded once.
- **Inventory and seats are reserved with single-statement conditional updates**, not
  read-then-write, and the concurrency is tested against real PostgreSQL rather than
  reasoned about.
- **The SQL dialect seam lives in `internal/persistence`.** Postgres-only is a
  deliberate choice with a cost written down in
  [docs/database-dialects.md](docs/database-dialects.md).
- **`internal/models` and `internal/types` are generated.** Do not hand-edit them;
  `make lint` fails the build if non-generated files appear there.

## Documentation

| | |
| --- | --- |
| [Running locally](docs/running-locally.md) | both workflows, configuration, tests, code generation |
| [Deployment](docs/deployment.md) | Render and Vercel, environment matrix |
| [Server initialization](docs/server-initialization.md) | startup order and wiring |
| [Database dialects](docs/database-dialects.md) | why Postgres only, and what changing that would cost |
| [Security audit](docs/security/2026-09-08-security-audit.md) | findings, what was fixed, what is deferred |
| [Commerce surface review](docs/security/2026-09-15-commerce-surface-review.md) | the payments, orders and ticketing review |
| [Migration guides](docs/migration-guides/) | inherited from upstream |

## Configuration

Everything is environment-driven. `make run-env` prints the fully resolved
configuration with secrets elided — the fastest way to see what the app actually
believes its settings are.

`DATABASE_URL` (the form every managed provider hands out) takes precedence over the
discrete `PG*` variables; `PG*` is what docker-compose supplies. Set
`MIGRATION_DATABASE_URL` to a direct, non-pooled endpoint when `DATABASE_URL` points
at a connection pooler.

`.env.local` overrides real environment variables, so `docker-compose.yml` sets
`DOTENV_DISABLE=true`. Without that, cloud credentials placed there for the no-Docker
workflow would silently redirect the containerised app — including `server --migrate`
— at the cloud database.

## Endpoints

| Path | Auth |
| --- | --- |
| `/api/v1/auth/*` | bearer token; login, register, refresh and forgot-password are open (and rate limited) |
| `/api/v1/membership/*` | bearer token; invitation acceptance is open |
| `/api/v1/events`, `/api/v1/events/:slug` | open; registration requires a bearer token |
| `/api/v1/products`, `/api/v1/checkout` | open read, authenticated checkout |
| `/api/v1/donations/*` | open; tiers, checkout and recurring giving |
| `/api/v1/faqs`, `/api/v1/contact-messages` | open |
| `/api/v1/admin/*` | bearer token carrying the `admin` scope |
| `/api/v1/push/*` | bearer token |
| `/webhooks/payments` | no bearer token — authenticated by provider signature over the raw body |
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
    handlers/ admin, auth, community, content, donations, merch, ticketing,
              push, webhooks, wellknown
  auth/       registration, login, tokens, password reset
  config/     all environment parsing
  data/       seed and fixture data
  mailer/     transactional email and transports
  metrics/    instrumentation
  models/     sqlboiler-generated (do not edit)
  payment/    provider interface, paystack and mock implementations
  persistence/ the SQL dialect seam
  storage/    S3 object storage and a mock
  types/      go-swagger-generated request/response types (do not edit)
  util/db/    query helpers (Postgres-specific)
migrations/   sql-migrate DDL
scripts/      gsdev developer tooling, behind the `scripts` build tag
web/          email + view templates, i18n bundles
.workflow/    ship project-lifecycle trail
```

## Testing and CI

```sh
make test           # all packages, with coverage
make test-by-name   # same, grouped by test name
make all            # clean, init, build, test and the security scans
```

GitHub Actions runs build and test, CodeQL analysis, and a `ship` gate that blocks a
phase whose artifact is incomplete or whose security findings are unresolved.
`make lint` additionally enforces that generated directories stay generated and that
implemented handlers still match the spec.

## Security

Report vulnerabilities per [SECURITY.md](SECURITY.md). Do not open public issues.

Known deferred items — token storage and refresh-token lifetime — are recorded in the
[security audit](docs/security/2026-09-08-security-audit.md) with the conditions under
which they must be addressed.

## Licence

MIT. See [LICENSE](LICENSE); the upstream go-starter copyright notice is retained as
the licence requires.
