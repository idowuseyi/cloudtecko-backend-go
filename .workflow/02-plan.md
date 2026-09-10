# 02 — Plan / Architecture

## Work Breakdown

| # | Package of work | New surface |
|---|---|---|
| 0 | Foundations | `admin` scope, admin bootstrap CLI, two transaction-bug fixes, Makefile NULL-check narrowing, config sections, logger redaction, job-runner decision |
| 1 | Content read path | `categories`, `media_assets`, `events`, `resources` tables; public events + resources endpoints; FTS |
| 2 | Admin CRUD + media | `/api/v1/admin/*` for events, resources, categories, FAQs; presigned upload + verify-on-complete |
| 3 | Contact, FAQ, membership | contact intake, FAQ read, waitlist apply/approve/reject, invitation accept-with-password, retention sweeper |
| 4 | Payment core | `internal/payment` Provider + paystack + mock; payments/donations/webhook tables; transition helpers; webhook endpoint |
| 5 | One-off donations | donation checkout, verify-on-return, receipts |
| 6 | Event ticketing | ticket tiers, registrations, seat reservation, free RSVP + paid |
| 7 | Merch | products, variants, orders, order items, stock reservation, fulfilment |
| 8 | Recurring donations | donation tiers as Paystack Plans, subscriptions, dunning, cancellation |
| 9 | Refunds & admin ops | refunds, restock, cancellation, dispute handling, unprocessed-webhook view |
| 10 | Verify & harden | concurrency load test, security review, deliverability, cron wiring |

## Architecture

The layering this fork has already grown (it is **not** stock go-starter — upstream handlers
query sqlboiler directly; here a service layer and a DTO boundary exist):

```
api/paths/*.yml + api/definitions/*.yml
        │  make swagger
        ▼
api/swagger.yml ──► internal/types/            (generated, committed, CI-diffed)
        │
        ▼
internal/api/handlers/<domain>/<method>_<name>.go
        │   func <Method><Name>Route(s *api.Server) *echo.Route
        │   bind → delegate → util.ValidateAndReturn
        ▼
internal/data/local/service_<domain>.go
        │   db.WithTransaction(ctx, s.db, func(exec boil.ContextExecutor) error { ... })
        │   ALWAYS exec, NEVER s.db, inside the closure
        ▼
internal/models/  (sqlboiler, generated)

dto in  → internal/data/dto      (+ ToTypes())
model out → internal/data/mapper
```

**Content model.** Two aggregates (`events`, `resources`), not one polymorphic `content_items`
table, with a shared `dto.GalleryItem` read projection and two mappers. Rationale in
`01-brief.md` § Options Considered; the decisive point is that `status: Open|Sold Out|Limited`
is derived from ticket-tier aggregates for events and meaningless for resources.

**Commerce concurrency.** One conditional-`UPDATE` transition helper guards the whole surface:
`UPDATE ... SET status = $new WHERE id = $1 AND status = ANY($2)` with `RowsAffected != 1` as
the error. Reservation is a single statement whose capacity predicate and counter increment
share one row lock. Expiry is lazy, evaluated at the top of every checkout transaction, because
this repository has no job runner.

**Payment→subject.** Nullable `order_id` / `donation_id` FKs plus a CHECK that exactly one is
set — preserving referential integrity and sqlboiler relations, and supporting a recurring
donation that produces many payments against one donation row.

## Tech Choices

| Choice | Decision | Why |
|---|---|---|
| State machines | Postgres enums | Forced: `default_zero_values.sql` rejects `text NOT NULL DEFAULT 'pending'` but never inspects `USER-DEFINED` types |
| Money | `bigint` kobo, `currency_code` enum pinned to NGN | No float arithmetic on money; enum passes the zero-value gate where `text DEFAULT 'NGN'` would fail |
| Search | `tsvector` generated column + GIN | One definition, no trigger drift. Two-arg `to_tsvector` only — the one-arg form is `STABLE` and is rejected in a generated column |
| Payments | Paystack behind an internal `Provider` interface, plus a `mock` | Mirrors `internal/push/provider`; makes the whole pipeline testable before a live key exists |
| Media | Presigned PUT to R2, then server-side `HeadObject` + content sniff before promotion | A presigned URL cannot constrain the bytes the client sends |
| Cart | Client-side (`zustand`), server revalidates at checkout | Front end already carries the dependency; server never trusts a client price |
| Deletes | `published_at` + `archived_at`, no `deleted_at` | A soft-delete filter one query forgets leaks content, and sqlboiler will not remind us |
| FKs to `users` | `ON DELETE SET NULL`, never CASCADE | `DeleteUserAccount` is a bare `DELETE FROM users`; a cascade would let an erasure request destroy financial records |

## Sequence & Milestones

Dependency order, with the risky spine first:

```
0 ──┬── 1 ── 2 ── 3
    └── 4 ── 5 ──┬── 6 ── 9
                 ├── 7 ──┘
                 └── 8
                              all ── 10
```

- **M1 (after 1):** `/events` and `/resources` render from the API; hardcoded arrays deleted.
- **M2 (after 3):** every form on the site persists; membership invitations work end to end.
- **M3 (after 5):** a real ₦100 moves in Paystack test mode, verified and replay-safe.
  **Gate on this before starting 6 or 7.**
- **M4 (after 7, 8):** all four money flows live.
- **M5 (after 10):** Verify gate clean, deployed to Render.

## Risks

| Risk | Severity | Mitigation |
|---|---|---|
| Webhook replay — Paystack signatures carry no timestamp, so an observed body verifies forever | High | Unique `(provider, idempotency_key)` insert **plus** conditional status transition; 200 on duplicate |
| Oversell under concurrent checkout | High | Single-statement reserve with predicate+increment under one row lock; load test in Phase 10 |
| Late webhook after a hold expired | Medium | `quantity_sold` deliberately allowed to exceed total; flag `oversold_at`; **never reject a captured payment** |
| The `s.db`-inside-transaction pattern copied into a webhook handler | High | Fixed in both existing files in Phase 0 + review-checklist item; it would mean double fulfilment |
| No job runner for expiry, retention, dunning, orphan sweeps | Medium | Lazy-on-request where correctness depends on it; cobra subcommand + Render cron as backstop |
| Enum evolution is irreversible (`ALTER TYPE ADD VALUE`) | Medium | Get state enums right in one pass; documented no-op Down convention |
| Presigned upload cannot enforce MIME/size | Medium | Quarantine prefix → `HeadObject` + sniff → promote; SVG banned; media served off-origin |
| `SearchStringToTSQuery` un-sanitised on a public GET | Medium | Sanitise to `[\p{L}\p{N}_-]` and degrade tsquery failure to empty results — otherwise a fuzzable 500 |
| Codegen drift failing CI | Low | `make build` before every commit; regeneration runs in the dev container |
| Shipping cost and VAT undefined | Low (now), blocks Phase 7 | Carried as open decisions; `weight_grams` and a zero-valued `tax_kobo` keep both options open |

## Sources

- Architecture, layering and codegen pipeline as described — `internal/api/handlers/push/put_update_push_token.go`, `internal/data/local/service_push.go`, `Makefile:273`, `scripts/internal/handlers/gen.go` — 2026-09-09
- Enum vs text forced by the zero-value gate — `scripts/sql/default_zero_values.sql:45-52` — 2026-09-09
- `IS NULL` banned in migrations — `Makefile:235-238` — 2026-09-09
- Every FK requires an index — `scripts/sql/fk_missing_index.sql`, `Makefile:249-252` — 2026-09-09
- `DeleteUserAccount` relies on FK cascades — `internal/auth/service.go:665` — 2026-09-09
- Paystack: HMAC-SHA512 hex over raw body in `x-paystack-signature`; **no timestamp**, no event ID; dedupe on event + `data.id`/`reference`; 30s timeout; retries every 3 min ×4 then hourly to 72 h — https://hookdeck.com/webhooks/platforms/guide-to-paystack-webhooks-features-and-best-practices — 2026-09-10
- Paystack subscriptions need a Plan; cancel requires `subscription_code` + `email_token` (returned once) — https://github.com/PaystackHQ/documentation/blob/master/recurring-payments/subscriptions.md — 2026-09-10
- `to_tsvector` one-arg form is `STABLE` and rejected in generated columns; two-arg form is `IMMUTABLE` — PostgreSQL docs, generated columns require IMMUTABLE expressions — 2026-09-10 [verified against repo behaviour at implementation time in Phase 1]
