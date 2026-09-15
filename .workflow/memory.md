# Workflow Memory — verified, non-obvious learnings
<!-- Read this file at the start of every phase (right after state.md). -->
<!-- Update at every phase Handoff: APPEND new non-obvious learnings AND
     PRUNE — remove entries proven false or dismissed; remove or rewrite
     entries whose concern has been attended to; re-verify or drop anything
     no longer confirmable. This file holds ONLY what is CURRENTLY VERIFIED
     TRUE — a curated set, not a journal. History lives in git and the phase
     artifacts. -->
<!-- Never record what is derivable from the repo, the artifacts, or git
     history. If something derivable seems worth saving, keep only what was
     surprising or non-obvious about it. Record confirmations as well as
     corrections. NEVER store secrets or credentials here — the secret scan
     covers this directory too. -->
<!-- Entry format:
- <date> — <phase> — <the learning>.
  Why: <why it matters / what breaks without it>
  How to apply: <the concrete behavior next time>
-->

- 2026-09-08 — bootstrap — A full security audit exists at
  `docs/security/2026-09-08-security-audit.md`, written in this workflow's finding format.
  Why: Verify must not redo it from scratch.
  How to apply: at Verify, re-check only what changed since, and lift the still-open findings
  (S-020, S-022, S-023) into `05-verify.md`.

- 2026-09-10 — design — S-018 and S-019 are **fixed**, not deferred. The previous entry here
  claimed they were deferred until "the first production deployment holding real user data" and
  told the Verify phase to act on that; the audit marks both `fixed` (lines 67, 69) and commits
  f40a511/74fa6cb implemented them.
  Why: carrying it forward would have made the GTM gate re-litigate finished work, and would
  have hidden the item that IS outstanding.
  How to apply: the real pre-production decision is the **password pepper**, recorded in the
  audit's non-code follow-ups — adding one after members exist forces a rehash on next login.

- 2026-09-10 — design — `internal/data/local/service_push.go` uses a partial commit ON PURPOSE:
  an unknown old push token still returns 404, but the NEW token is kept, because a device with
  a valid new token must keep receiving pushes. `ErrNotFoundOldPushToken`'s message says so and
  a test asserts it.
  Why: it reads exactly like the transaction-escape bug in `internal/auth/service.go`, and
  "fixing" it breaks a passing test and silently disables push delivery for rotating devices.
  How to apply: before repairing an `s.db`-inside-transaction, check whether the error message
  or a test documents the partial commit as intended.

- 2026-09-10 — design — The dev stack must be started to run the DB tests
  (`docker compose up -d postgres integresql service`), and `sql-migrate`/`sqlboiler`/`wire` are
  go.mod `tool` directives, NOT installed in the image — `go build -o /go/bin/<name> <pkg>` for
  each, or the Makefile's bare `sqlboiler psql` / `sql-migrate up` fail with "command not found".
  Why: from the host 29 of 31 packages fail on `lookup integresql: no such host`; inside the
  container all 23 pass. The distinction is not "some tests are flaky".
  How to apply: run `go test ./...` via `docker compose exec -T service`, never on the host.

- 2026-09-15 — implement — A failed statement ABORTS the whole transaction in
  PostgreSQL. Catching a 23505 and then running another query in the same
  transaction fails with "current transaction is aborted".
  Why: it bit twice. Slug allocation cannot be insert-and-retry without a
  SAVEPOINT, and the webhook de-duplication guard turned a duplicate delivery
  into a 500 — which made the provider retry the duplicate, i.e. the guard
  against redelivery caused redelivery.
  How to apply: use `INSERT ... ON CONFLICT DO NOTHING RETURNING id` and treat
  zero rows as "already there". Never catch a constraint error and continue in
  the same transaction.

- 2026-09-15 — implement — Capacity must be enforced in the UPDATE's WHERE
  clause, never as a SELECT-then-UPDATE and never as a CHECK.
  Why: reverting to SELECT-then-UPDATE let all 10 concurrent buyers take seats
  on a 3-seat tier. A CHECK would be wrong in the other direction: a captured
  payment whose hold expired MUST be allowed to exceed capacity, or the response
  is non-2xx and the money is taken with nothing recorded.
  How to apply: single statement, `WHERE ... AND reserved + sold + $n <= total`,
  `RowsAffected != 1` is a 409. Commerce code must stay at READ COMMITTED — at
  REPEATABLE READ the same statement raises 40001 and the contract becomes a 500.

- 2026-09-15 — implement — This repo's `util.BindAndValidate*` helpers bind path
  and query params ONLY, never headers.
  Why: go-swagger still generates validation for a declared header parameter, so
  a `required: true` header is validated against a value nothing populates and
  returns 400 on every request, correct ones included.
  How to apply: declare header params as NOT required and read them with
  `c.Request().Header.Get(...)` in the handler.

- 2026-09-15 — implement — A config field can be declared, consumed, and never
  assigned from the environment; it then silently holds the zero value.
  Why: `InvitationTokenValidity` was used by approval and never read from env, so
  every invitation was born already expired. It compiled, vetted and passed every
  test, because the fixtures set the column directly and no test crossed the
  approve→accept seam.
  How to apply: test the SEAM between two features, not just each side. Cross-check
  `Server` struct fields against `util.GetEnv*` call sites when adding config.

- 2026-09-15 — implement — Only ONE credential here is encrypted rather than
  hashed: a subscription's provider email token (`internal/util/secretbox`).
  Why: Paystack returns it once and requires it back to cancel, so hashing it
  would mean a donor can never stop giving through our own UI.
  How to apply: do not reach for secretbox for anything else. Everything else
  hashes, per S-018/S-024.
