# Security audit — 2026-09-08

Scope: the whole repository at the point it was adopted from the
[allaboutapps/go-starter](https://github.com/allaboutapps/go-starter) template
(commit `3d195ee`, unmodified), plus the changes made during adoption.

This was the first review of the codebase as ours rather than as a template.
Findings use ship's `core/_security-review.md` format, so they can be lifted
into `.workflow/05-verify.md` verbatim when the Verify phase runs:

```
S-NNN — title — severity — file:line — exploit scenario — fix — status
```

## Tooling

| Scanner | Version | Result |
| --- | --- | --- |
| `govulncheck` | v1.7.0 | 0 called vulnerabilities (1 imported, 3 in required modules, none reachable) |
| `trivy fs` (vuln, secret, misconfig) | latest | 1 HIGH in `go.mod`, 3 HIGH Dockerfile misconfigs |
| `gitleaks` | latest | 17 findings, all test fixtures — 0 real secrets |
| `golangci-lint` (gosec enabled) | v2.13.0 | 0 issues |

**No real credentials are committed.** All 473 tracked files were scanned for
AWS/GitHub/OpenAI/Slack/Google keys and PEM blocks: zero hits. Everything
gitleaks reported is a static test fixture (`dbuser`/`dbpass`, `mgmtpass`,
argon2id hashes, UUID tokens) that must stay committed for the suite to run.
`.gitleaks.toml` now allowlists exactly those, taking the scan to "no leaks
found" so a genuine leak will stand out.

## Findings

S-001 — echo affected by CVE-2026-55677, unauthorized information disclosure via URL path decoding discrepancy — High — go.mod:29 — the flaw is in request routing, so every endpoint is in scope; a crafted path can be decoded differently by the router than by middleware, letting a request reach a handler the middleware believed it had rejected — upgraded echo v4.13.4 → v4.15.4 — fixed

S-002 — login leaks account existence through response timing — High — internal/auth/service.go:256 — an unknown username returned immediately after the failed database lookup while a real one paid a full Argon2id derivation; measured against the pre-fix binary this was 1.4 ms vs 24.1 ms, a 17x gap, giving anyone an unauthenticated, remotely observable oracle for enumerating registered accounts — perform an equivalent dummy Argon2 derivation on the unknown-user path (internal/util/hashing/dummy.go); measured after the fix: 17.9 ms vs 19.3 ms — fixed

S-003 — CORS allowed every origin by default with no way to narrow it — High — internal/api/router/router.go:152 — echo's zero-config `CORS()` is `AllowOrigins: ["*"]` and was enabled by default, so any website could issue cross-origin requests to this API; combined with a future cookie-based session this becomes a full CSRF/read primitive — replaced with `CORSWithConfig` driven by `SERVER_ECHO_CORS_ALLOW_ORIGINS`, empty means CORS stays off rather than allowing everyone — fixed

S-004 — no rate limiting on any endpoint — High — internal/api/router/router.go — login, register and forgot-password were completely unthrottled, so password guessing, account enumeration and mail-bombing via password reset had no cost; there is also no account lockout anywhere in the codebase — per-IP rate limiter on `/api/v1/auth` (verified: burst allowed, then 429) — fixed

S-005 — `/metrics` was published outside the management group — High — internal/api/router/router.go:248 — the route was registered on the root echo instance rather than the `/-` group, so whenever `SERVER_MANAGEMENT_ENABLE_METRICS` was on, per-route traffic volumes, latency histograms and total user count were readable by anyone with no secret at all — moved onto the Management group behind the mgmt-secret key auth (verified: `/metrics` 404, `/-/metrics` 400 without the secret, 200 with it) — fixed

S-006 — pprof could be served unauthenticated, and hung off the global mux — High — internal/api/router/router.go:165 — setting `SERVER_PPROF_ENABLE_MANAGEMENT_KEY_AUTH=false` silently swapped the key auth for a noop, publishing heap and goroutine dumps that routinely contain tokens, passwords and connection strings; it also wrapped `http.DefaultServeMux`, the process-global mux any dependency can register handlers on — that combination now refuses to boot outside debug, and pprof is served from a private mux — fixed

S-007 — the generated management secret was written to the log in cleartext — High — internal/util/env.go:199 — when `SERVER_MANAGEMENT_SECRET` is unset a random secret is generated and was then logged at WARN in full; that secret grants access to every management endpoint, and logs are routinely shipped to third-party aggregators, so anyone with log access had management access — log only that a secret was generated, plus its length — fixed

S-008 — CI printed database and management credentials into the build log — High — .drone.yml:252, .woodpecker/ci.yaml:195 — both pipelines wrote the step environment to `.hostenv` and then `cat`-ed it, exposing `PGPASSWORD` and `SERVER_MANAGEMENT_SECRET` to anyone who can read a build log — print only the variable count — fixed

S-009 — no HTTP server timeouts — Medium — internal/api/server.go:123 — echo's `*http.Server` ships with every timeout at zero, i.e. unlimited, so a client that opens a connection and dribbles out a request header holds a goroutine and a file descriptor indefinitely (Slowloris); a few thousand such connections exhaust the process — Read/ReadHeader/Write/Idle timeouts and MaxHeaderBytes are now configured — fixed

S-010 — no request body size limit — Medium — internal/api/router/router.go — echo's default is unlimited, and the `maxLength` constraints in the swagger spec are only enforced after the body has been fully read into memory, so a single large POST could drive memory exhaustion — `SERVER_ECHO_BODY_LIMIT`, default 1M (verified: 413 on oversize) — fixed

S-011 — Argon2 tuning was dead configuration — Medium — internal/auth/service.go:82,408 — `DefaultArgon2ParamsFromEnv()` existed and read five `AUTH_HASHING_ARGON2_*` variables, but both call sites passed the hardcoded `DefaultArgon2Params`, so operators who tuned the cost factors silently got the defaults anyway — wired the env-derived params in at both call sites — fixed

S-012 — HSTS disabled and Referrer-Policy unset by default — Medium — internal/config/server_config.go:172,178 — `HSTSMaxAge` defaulted to 0 (header never sent) and `ReferrerPolicy` to empty, so a first request could be downgraded to plaintext and full URLs leaked to third-party sites via the Referer header — defaults are now 31536000 and `strict-origin-when-cross-origin` — fixed

S-013 — the full API specification was served unauthenticated with no way to disable it — Low — internal/api/handlers/common/get_swagger.go:19 — `/swagger.yml` exposes every route, parameter, and error shape, which is a ready-made map for an attacker — added `SERVER_ECHO_ENABLE_PUBLIC_SWAGGER`; it defaults to true to preserve local development (the docker-compose swaggerui service reads it) and is set to false in both deployment configs — fixed

S-014 — the app container ran as root — Medium — Dockerfile:259 (Trivy DS-0002) — the distroless base defaults to uid 0, so any code-execution bug starts with root inside the container, making an escape materially cheaper; nothing in this service needs root — runs as uid 65532, with `assets/` chown'd because the liveness probe writes `assets/mnt/.healthy` — fixed

S-015 — a developer's `.env.local` could silently redirect the Docker stack at a production database — High — internal/config/dot_env.go:51 — `.env.local` is applied with `os.Setenv`, so it *overrides* real environment variables; once managed-database credentials are placed there for the no-Docker workflow, `docker compose up` would connect the containerised app to that cloud database instead of the local container, and `server --migrate` would run migrations against it — this is a data-loss path, not merely a surprise — added `DOTENV_DISABLE`, set in docker-compose.yml — fixed

S-016 — unpinned CI actions and a piped remote installer — Medium — .github/workflows/build-test.yml:47,66 — `aquasecurity/trivy-action@master` means the security scanner itself changes without review, and `bash <(curl -s https://codecov.io/bash)` pipes an unpinned remote script into a shell that holds repository credentials — pinned the action and replaced the installer with `codecov/codecov-action@v5` — fixed

S-017 — govulncheck was installed but never executed — Medium — Dockerfile:149, Makefile — the tool was present in the image and had a make target, but no pipeline invoked it and it was not part of `make all`, so vulnerable dependencies could land unnoticed — wired into `make all` and all three CI pipelines — fixed

S-018 — access and refresh tokens were stored in plaintext — Medium — migrations/20200428072541-create-access-tokens.sql:3 — the token column was the primary key with a `uuid_generate_v4()` default, so the value handed to the client was stored verbatim; any read of that table (SQL injection, a leaked backup, an over-privileged analytics user) was immediate session takeover for every active user, with a 24 hour default validity — migration 20260908090000 replaces `token` with `token_hash` (SHA-256) on both tables; tokens are generated in Go by `hashing.NewToken()` and only the hash is persisted. SHA-256 rather than Argon2 is deliberate: these are 122-bit CSPRNG values with nothing to brute force, and the hash is computed on every authenticated request, where Argon2 would be a denial-of-service surface. Verified live: the plaintext appears in 0 rows, the stored hash equals sha256(client token), and authentication still succeeds — fixed

S-019 — refresh tokens never expired and had no reuse detection — Medium — migrations/20200428072639-create-refresh-tokens.sql:2 — the table held only `token`, `user_id`, `created_at`, `updated_at`: no expiry column at all, so a stolen refresh token was valid forever, and rotation deleted the old row, making a replayed token indistinguishable from one that never existed — added `valid_until` (30 days, `SERVER_AUTH_REFRESH_TOKEN_VALIDITY`), `family_id` and `consumed_at`. Rotation marks the old token consumed instead of deleting it, and presenting a consumed token revokes the whole family plus every access token of that user. Verified live: rotation left both rows in one family with one consumed; replaying the consumed token returned 401 and also invalidated the legitimately rotated token and all access tokens — fixed

S-020 — registration discloses whether an account exists — Low — internal/auth/service.go:374 — `/register` returns `ErrConflictUserAlreadyExists` for a taken username, so the endpoint can be used to test addresses; note this is inconsistent with `/forgot-password`, which the template deliberately made non-disclosing — return a generic success and send a "you already have an account" email instead — deferred(product decision — it changes the signup UX and needs a call from the product owner)

S-021 — no password policy — Low — api/definitions/auth.yml:57,63,75,104 — every password field was `minLength: 1`, so a single-character password was accepted at registration, change-password and reset — raised to `minLength: 15` on the three fields that SET a password (register `password`, change-password `newPassword`, forgot-password-complete `password`) and regenerated `internal/types` with go-swagger in the dev container. 15 is NIST SP 800-63B Rev 4's requirement for a password used as the only authentication factor, which is the case here (there is no MFA); OWASP ASVS 5.0 sets its floor at 8 but likewise recommends 15.

  The three VERIFY fields (login `password`, and `currentPassword` on
  change-password and delete-account) deliberately keep `minLength: 1`. Enforcing
  a length policy where a password is checked rather than chosen would reject an
  existing user's shorter password at input validation, before the credential
  check ever runs — locking them out instead of letting them authenticate and
  then choose a longer one. Tests assert both halves: a 14-character
  registration is rejected while 15 succeeds, and a one-character login returns
  401 (wrong credentials) rather than 400 (invalid input).

  A dictionary/breach strength check is a separate, larger piece of work and is
  not included — length alone does not stop "aaaaaaaaaaaaaaa" — deferred(strength check only; the length policy is fixed. Flip condition: before onboarding real users, add a breached-password check such as HaveIBeenPwned's k-anonymity range API)

S-022 — unused database drivers are compiled into the binary — Low — go.mod — `sql-migrate` and `sqlboiler` transitively pull in the mssql, oci8, sqlite3 and mysql drivers, and `go version -m` confirms they ship in the binary, enlarging the attack surface for code that can never be reached; `go.not` only *asserts* absence, so listing them there would fail `make check-embedded-modules-go-not` rather than remove them — no clean fix without replacing sql-migrate — deferred(accepted risk; flip condition: if a CVE is published against any of these drivers)

S-023 — stale direct dependencies — Low — go.mod:41,32 — `github.com/timewasted/go-accept-headers` is a 2013 pseudo-version and `github.com/pkg/errors` is archived upstream, so neither will receive a security fix — replace with maintained equivalents — deferred(no known vulnerability today; flip condition: any advisory against either)

S-024 — password-reset and confirmation tokens were stored in plaintext — Medium — migrations/20200428072651-create-password-reset-tokens.sql:3, migrations/20250505074511-create-table-confirmation-tokens.sql:3 — the same exposure as S-018, and arguably worse: a password-reset token is a direct path to account takeover, and being short-lived (15 minutes) and single-use narrows the window without closing it. Anyone who could read the table could reset any account — migration 20260908100000 replaces `token` with `token_hash` (SHA-256) on both tables; tokens are generated by `hashing.NewToken()` and only the hash is stored. Verified live: all four token tables now have zero plaintext `token` columns — fixed

**Behaviour change forced by S-024.** The password-reset *reuse window* re-sent
the same token when a user asked twice within `PasswordResetTokenReuseDuration`.
That requires reading the plaintext back, which a one-way hash makes impossible.
Requesting a reset now **invalidates any earlier one**: exactly one live reset
token per user, which is the stronger option and matches what users expect from
password reset elsewhere. The consequence to be aware of is that if the first
email arrives late and the user clicks it after requesting a second, that link is
already dead. `SERVER_AUTH_PASSWORD_RESET_TOKEN_REUSE_DURATION_SECONDS` has been
removed rather than left inert — a config knob that silently does nothing is
worse than one that is gone. Its default was already `0`, so deployments using
the defaults see no change. Verified live: a retry inside the debounce is
ignored, and a retry past it leaves exactly one row carrying a different hash.

## Reviewed and found solid

- **Password hashing.** Argon2id via `argon2.IDKey`, salt from `crypto/rand`, and
  `ComparePasswordAndHash` uses `crypto/subtle.ConstantTimeCompare` — a correct
  constant-time comparison, not a naive `==`.
- **Committed secrets.** None. See the tooling table above.
- **SQL injection.** The data layer is sqlboiler-generated and parameterised
  throughout. The helpers in `internal/util/db/` interpolate only *column names*
  (from generated constants) and pass values as bind parameters. There is exactly
  one raw query on a request path, `SELECT nextval('seq_health')`
  (internal/api/handlers/common/probes.go:139), which takes no user input.
- **Credential redaction in config dumps.** `app env` elides `Database.Password`,
  `SMTP.Password`, the management secret and the FCM credentials path via
  `json:"-"`. Verified live: the resolved config prints host, port, user and
  database but no password.
- **Auth logging.** Request and response body logging is force-skipped for
  `/api/v1/auth/*`, and `Authorization`, `X-CSRF-Token` and `Proxy-Authorization`
  headers are redacted (internal/api/middleware/logger.go:76).
- **Forgot-password enumeration.** Deliberately non-disclosing
  (internal/api/handlers/auth/post_forgot_password.go:38), with a debounce on
  resend.
- **Token entropy.** UUIDv4 from `uuid-ossp` is 122 bits — adequate. The problem
  is storage (S-018), not generation.
- **Bearer auth enforcement.** Verified live: `/api/v1/auth/userinfo` returns 401
  without a token, 400 for a malformed one and 200 with a valid one; deactivated
  users and scope mismatches are rejected in middleware.

## Not covered

- **No penetration testing or fuzzing** was performed — this is a code and
  configuration review plus automated scanning.
- **The full test suite was not executed.** 15 of 47 packages require a live
  IntegreSQL server, which is not reachable from the host (it returns connection
  details naming the compose-internal hostname `postgres`). Build, vet,
  golangci-lint and the 32 host-runnable packages all pass; the database-backed
  handler tests must run in the dev container or CI.
- **`internal/models` tests were not run** — they need the sqlboiler `PSQL_*`
  spec database, which was not provisioned.
- **No review of the Drone/Woodpecker deployment steps**, which were removed
  during adoption rather than audited (they targeted the upstream vendor's
  infrastructure).
- **Dependency licence review** was not repeated; `make get-licenses` still runs
  in CI.
- **Trivy's remaining two Dockerfile findings** (DS-0029, missing
  `--no-install-recommends`) are image-size hygiene in the *development* stage
  only and were left alone; that stage is never published.

## Non-code follow-ups

- Nothing needs rotating: no real credential was ever committed, so there is no
  leaked secret to revoke.
- `SERVER_MANAGEMENT_SECRET` must be set explicitly in every deployment.
  Unset, a random secret is generated per process, so it differs between
  instances and changes on every restart. Both deployment configs mark it as a
  required, non-synced secret.
- S-018 and S-019 are fixed by migration 20260908090000. It rehashes existing
  rows in place, but **every live refresh token receives a validity measured from
  its creation date**, so any token older than 30 days stops working the moment
  the migration is deployed and those users must log in again.
- Password-reset and confirmation tokens are now hashed too (S-024), so no
  credential-bearing token is stored in plaintext anywhere. **That change removed
  the password-reset reuse window** -- see S-024 for the behaviour consequence.

## Sources

- CVE-2026-55677, echo unauthorized information disclosure via URL path decoding discrepancy, fixed in 4.15.3 — `trivy fs` against go.mod, and https://avd.aquasec.com/nvd/cve-2026-55677 — 2026-09-08
- Go vulnerability database state — `govulncheck` DB updated 2026-09-02 — 2026-09-08
- Trivy DS-0002 (run containers as a non-root user) — https://avd.aquasec.com/misconfig/ds-0002 — 2026-09-08
- Login timing figures — measured locally, 12 requests per case, against a binary built from commit `b5b8e85` (pre-fix) and the working tree (post-fix) — 2026-09-08
- Plaintext token storage and the absence of a refresh-token expiry column — observed directly via `psql` against the migrated database — 2026-09-08
- NIST SP 800-63B Rev 4 (finalized July 2025): 15-character minimum when a password is the only authentication factor, 8 when combined with MFA; verifiers should accept at least 64 — web search of NIST guidance summaries — 2026-09-08
- OWASP ASVS 5.0 lowered its L1 floor to 8 to align with NIST Rev 4 while strongly recommending 15 (ASVS 4.0.3 had required 12) — web search — 2026-09-08
- Vercel Functions limits (1,024 shared file descriptors, 4.5 MB request body) — https://vercel.com/docs/functions/limitations — 2026-09-08
- Vercel container services and the required catch-all rewrite — https://vercel.com/kb/guide/deploy-go-using-docker-vercel — 2026-09-08
