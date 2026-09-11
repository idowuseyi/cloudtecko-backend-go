# 04 — Implementation Log

Phases 0 and 1 of the eleven-phase plan. Everything below is verified in the dev
container: `make build` green (0 lint issues) and 23/23 test packages passing.

## Files Changed

**Phase 0 — foundations**

| File | Change |
|---|---|
| `internal/auth/service.go` | `CompleteRegister` read moved from `s.db` to `exec` **and** given `FOR UPDATE OF confirmation_tokens` |
| `internal/data/local/service_push.go` | Split into two explicit transactions, preserving the intentional partial commit |
| `Makefile:235` | NULL check narrowed to exclude `IS NULL` and `NULLS ` |
| `migrations/20260910090000-add-admin-user-scope.sql` | `user_scope` recreated with `admin` |
| `internal/auth/scopes.go` | `ScopeAdmin` |
| `api/definitions/auth.yml` | `UserScope` enum gains `admin` |
| `cmd/db/grant_scope.go`, `cmd/db/db.go` | `app db grant-scope --username X --scope admin [--revoke]` |

**Phase 1 — content read path**

| File | Change |
|---|---|
| `migrations/20260910100000-create-content-taxonomy.sql` | `categories`, `media_assets` + 2 enums |
| `migrations/20260910101000-create-events.sql` | `events`, incl. the single-featured partial unique index |
| `migrations/20260910102000-create-resources.sql` | `resources` + generated `tsvector` + GIN index |
| `sqlboiler.toml` | blacklists `resources.search_tsv`; disables generated model tests |
| `internal/util/db/ts_vector.go` | allow-list sanitiser + `SearchTSVector`/`SearchTSVectorRank` mods |
| `internal/data/dto/gallery.go` | `GalleryItem`/`GalleryPage`/`EventDetail`/`ResourceDetail`, `DeriveGalleryStatus` |
| `internal/data/mapper/content.go` | model → DTO/gallery projections |
| `internal/data/local/service_content.go` | list/detail/featured/search queries |
| `internal/api/handlers/content/*.go` | 5 handlers + 2 test files |
| `internal/api/httperrors/content.go` | `ErrNotFoundEvent`, `ErrNotFoundResource` |
| `internal/api/server.go`, `internal/api/router/router.go` | `APIV1Content` group |
| `api/definitions/content.yml`, `api/paths/content.yml` | public content contract |
| `api/templates/server/parameter.gotmpl` | **nil guard in generated param validators** (see below) |
| `internal/test/fixtures/fixtures.go` | 12 content fixtures, in FK order |
| `internal/data/fixtures/fixtures.go` | 19 seed rows mirroring the frontend's hardcoded arrays |

**Phase 2 — admin authoring and media**

| File | Change |
|---|---|
| `internal/api/handlers/admin/*.go` | 16 handlers (14 content + 2 media) + 3 test files |
| `internal/data/local/service_content_admin*.go` | event/resource/category write services |
| `internal/data/local/service_media.go` | presign, verify-on-complete, reject-and-delete |
| `internal/storage/` | `Storage` interface + `s3/` (minio-go) + `mock/` |
| `internal/util/slug/` | slug derivation, reserved words, collision suffixes |
| `internal/util/db/errors.go` | SQLSTATE classification (unique / FK / restrict / check) |
| `internal/config/server_config.go` | `Storage` config section |
| `internal/api/{server,providers,wire}.go` | storage wired through DI |

**Phase 3 — contact, FAQs, membership**

| File | Change |
|---|---|
| `migrations/20260911090000-create-contact-and-membership.sql` | `contact_messages`, `faqs`, `membership_applications` + 3 enums |
| `internal/data/local/service_community.go` | contact intake + throttle, FAQ reads, applications, retention sweep |
| `internal/auth/service_membership.go` | approve (mints hashed invitation), accept (creates the account) |
| `internal/api/handlers/community/*.go` | 4 public handlers + tests |
| `internal/api/handlers/admin/*application*.go` | list / approve / reject |
| `internal/mailer/mailer.go` + `web/templates/email/` | invitation + account-exists emails |
| `cmd/db/purge_expired.go` | `app db purge-expired` |
| `internal/api/server.go`, `wire.go` | `MembershipService` split out of `AuthService` |

## Key Decisions

**A nil-dereference panic in generated code was fixed at the template, not the call site.**
`GET /api/v1/resources` returned 500 whenever `?q=` was omitted: the generated `Validate()`
calls every `validate<Param>` unconditionally, while the validator body dereferences the
pointer. Any optional query parameter carrying **any** validation (`maxLength`, `minimum`,
`pattern`, `format`) would panic. Fixed in `api/templates/server/parameter.gotmpl`, which this
repo already overrides, so the whole class is closed rather than this one parameter. Regenerating
produced **zero** diff in existing parameter files — no current endpoint had an optional
validated query param — so the blast radius is nil. Tests assert both halves: absent `q` returns
200, and `q` of 201 characters still returns 400.

**Generated sqlboiler model tests are disabled.** They build rows with `randomize.Struct`, which
cannot know about CHECK constraints or partial unique indexes, so random `start_at`/`end_at`
tripped `events_time_order_check` and two randomly-featured rows tripped
`idx_events_single_featured`. They were asserting that any random tuple is insertable — the exact
opposite of what those constraints exist to enforce — and the problem compounds with every
constraint the commerce schema adds. They exercised sqlboiler's codegen, not this project's
logic; real coverage is the handler tests against IntegreSQL. Recorded in `sqlboiler.toml`.

**The search sanitiser is an allow-list.** The real break was not the tsquery operators I first
assumed — `)`, `|`, `&` are inert inside a quoted lexeme — but a **trailing backslash**, which
escapes the closing quote and yields `'foo\':*`, a syntax error and therefore a fuzzable 500 on
a public GET. Enumerating dangerous characters is how that was missed once; the allow-list
cannot miss. A term must also carry at least one letter or digit, so `-` alone does not emit a
lexeme-free `'-':*`.

**One featured event is a database invariant.** `idx_events_single_featured` is a partial unique
index on `(is_featured) WHERE is_featured AND published_at IS NOT NULL`. Verified by insert:
the second featured row is refused with 23505.

**`status` is derived, never stored.** `DeriveGalleryStatus(sold, capacity)` runs at read time.
Ticketing arrives in Phase 6; until then callers pass 0/0, which reads Open — correct for an
event that does not sell tickets. A test asserts resources carry no status at all.

**Media verification happens after the upload, because nothing else can.**
A presigned PUT signs method, bucket, key and expiry -- it cannot bind Content-Type or size,
and the client chooses the bytes. So `PresignPut` deliberately takes **no** contentType
parameter: accepting one would promise an enforcement no S3-compatible store can provide and
invite callers to skip the checks that do work. Completion HEADs the object for its true size,
sniffs the leading 3KB for its true type, rejects anything outside the allow-list, deletes the
offending object, and only then promotes quarantine -> public. SVG is excluded outright: it
genuinely *is* `image/svg+xml`, so no MIME check can catch it.

**`ON DELETE RESTRICT` raises SQLSTATE 23001, not 23503.** Two distinct codes with identical
messages, so matching on message text would have hidden it permanently. `IsForeignKeyViolation`
covers both, with a test pinning the distinction.

**S-020 is closed, with a stated limit.** `/register` no longer answers 409 for a taken
address; it returns the byte-identical response a fresh registration gets, and the notice goes
by email to the mailbox owner. This only fully closes the oracle when registration requires
confirmation: with confirmation off a new address is answered with a **session**, which an
existing address cannot be given, and that difference is itself the signal. The server logs a
warning at startup in that configuration, `render.yaml` sets confirmation on, and
`TestPostRegisterDisclosesWhenConfirmationDisabled` pins the residual gap so it cannot be
quietly forgotten.

**Approval creates no account.** Only the SHA-256 of the invitation token is stored. The user
row is created at *acceptance*, with a password the applicant chooses. Verified live: after
approving a clean address, `users` still holds zero rows for it. The invitation link is
deliberately **not** logged when mail sending is disabled, unlike the password-reset link —
a reset link touches an existing account, an invite link creates one on an approval-gated site.

**The contact throttle is silent.** Answering "you submitted too recently" would confirm that an
address has used the form, so a throttled submission returns the same 202 as an accepted one.

## Deviations from Design

1. **`internal/data/local/service_push.go` was not a bug.** `01-brief.md`, `02-plan.md` and
   `03-design.md` all listed it as a second transaction-executor defect. It is a deliberate
   partial commit: `ErrNotFoundOldPushToken`'s message reads "The new token was saved." and
   `TestPutUpdatePushTokenWithOldTokenNotfound` asserts it. Fixing it broke a passing test.
   Restored as two explicit transactions; artifacts corrected; recorded in `memory.md`.
2. **The `admin` scope migration drops and recreates the enum** rather than `ALTER TYPE ... ADD
   VALUE`, which the design assumed. No column is of type `user_scope` (`users.scopes` is
   `text[]`), so the type has no dependents and recreation is safe — and unlike `ADD VALUE`, it
   is reversible, which this repo requires.
3. **`api/templates/server/parameter.gotmpl` was modified.** Not anticipated by the design; see
   Key Decisions.
4. **Generated model tests removed.** Not anticipated; see Key Decisions.
5. **`Storage.GetRange` was cut** in the design review and replaced by `Get` returning a
   reader, which the caller wraps in `io.LimitReader` for sniffing -- one method instead of two.
6. **A new dependency was added: `minio-go v7.3.0`.** Hand-rolling AWS SigV4 request signing is
   security-sensitive code that should not be written by hand, which outweighed the ~7
   transitive modules it brings. Its MVS resolution also bumped several unrelated shared
   dependencies (cobra, BurntSushi/toml, afero, fatih/color); `go mod tidy` alone produces no
   such bumps, so they are genuinely minio's, not pre-existing drift.
8. **`AuthService` was split.** Adding the two membership methods pushed it to 12 methods and
   `interfacebloat` flagged it. Rather than suppress, `MembershipService` is now a separate seam
   on `api.Server`, still implemented by `*auth.Service` and bound in wire — turning an approved
   application into an account is a different responsibility from signing an existing user in.
9. **`render.yaml` needed no change** — `SERVER_ECHO_CORS_ALLOW_ORIGINS` already exists as a
   prompted variable. It must still be **set at deploy time**: empty leaves CORS off entirely
   with no wildcard fallback, and the frontend then receives nothing.

## How to Run

```bash
docker compose up -d postgres integresql service

# sql-migrate, sqlboiler and wire are go.mod `tool` directives, NOT in the image:
docker compose exec -T service bash -c 'cd /app && \
  for t in "sql-migrate github.com/rubenv/sql-migrate/sql-migrate" \
           "sqlboiler github.com/aarondl/sqlboiler/v4" \
           "sqlboiler-psql github.com/aarondl/sqlboiler/v4/drivers/sqlboiler-psql" \
           "wire github.com/google/wire/cmd/wire"; do \
    set -- $t; go build -o /go/bin/$1 $2; done'

docker compose exec -T service bash -c 'cd /app && make build'       # codegen + lint + build
docker compose exec -T service bash -c 'cd /app && go test ./...'    # 23/23 packages

docker compose exec -T service bash -c 'cd /app && sql-migrate up -env development && ./bin/app db seed'
docker compose exec -d service bash -c 'cd /app && SERVER_ECHO_CORS_ALLOW_ORIGINS=http://localhost:3000 ./bin/app server'

curl -s "http://localhost:8080/api/v1/events?perPage=2"
curl -s  http://localhost:8080/api/v1/events/featured
curl -s "http://localhost:8080/api/v1/resources?q=serverless"
```

Never run `go test` on the host: IntegreSQL is unreachable there and 29 of 31 packages fail on
`lookup integresql: no such host`, which looks like broken tests rather than a missing service.

## Sources

- Build, lint and full test suite green in the dev container — `docker compose exec -T service bash -c 'cd /app && make build && go test ./...'` — 2026-09-10
- Single-featured invariant and `end_at >= start_at` enforced — direct `psql` inserts against the spec DB returning 23505 and 23514 — 2026-09-10
- Weighted FTS ranks a title match above a body mention (0.60793 vs 0.06079) — `psql` `ts_rank` query — 2026-09-10
- Trailing backslash produces `syntax error in tsquery: "'foo\':*"` — `psql -c "SELECT to_tsquery('english', '''foo\'':*')"` — 2026-09-10
- Optional-param panic located at generated `validateQ` — echo PANIC RECOVER stack, `internal/types/content/get_resources_route_parameters.go:254` — 2026-09-10
- Template fix produced no diff in existing parameter files — `git diff --stat internal/types/` — 2026-09-10
- Generated model tests are marked "meant to be re-generated in place and/or deleted at any time" — `internal/models/events_test.go:2` — 2026-09-10
- Live API returns the frontend's `ResourceItem` shape, CORS negotiates for `http://localhost:3000`, hostile search inputs return 200, unpublished content 404s — `curl` against the running server — 2026-09-10
