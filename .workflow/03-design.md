# 03 — Design

## Components

New packages and where they sit. Everything follows the existing seam: handler binds and
delegates, service owns the transaction, dto/mapper at the boundary.

| Component | Path | Responsibility |
|---|---|---|
| Content service | `internal/data/local/service_content.go` | events + resources reads, gallery projection |
| Content admin service | `internal/data/local/service_content_admin.go` | CRUD, slug allocation, publish/archive |
| Community service | `internal/data/local/service_community.go` | contact messages, FAQs, membership applications + invitations |
| Commerce service | `internal/data/local/service_commerce.go` | carts→orders, stock + seat reservation, fulfilment |
| Donation service | `internal/data/local/service_donations.go` | one-off and recurring donations |
| Payment provider | `internal/payment/` | `Provider` interface, `paystack/`, `mock/` |
| Object storage | `internal/storage/` | `Storage` interface, `r2/` (S3-compatible), `mock/` |
| Transition helpers | `internal/data/local/transitions.go` | the conditional-UPDATE state guard used by all commerce |
| Gallery DTO | `internal/data/dto/gallery.go` | `GalleryItem` read projection + `ToTypes()` |

New router groups on `api.Router` (`internal/api/server.go`) and `router.Init`:

| Group | Path | Middleware |
|---|---|---|
| `APIV1Content` | `/api/v1` (events, resources, faqs) | `AuthModeNone`, cache-control |
| `APIV1Community` | `/api/v1` (contact, membership) | `AuthModeNone` + **rate limiter** |
| `APIV1Commerce` | `/api/v1` (checkout, orders, donations) | `AuthModeTry` + **rate limiter** |
| `APIV1Admin` | `/api/v1/admin` | `middleware.AuthWithConfig`, `Scopes: ["admin"]` only |
| `Webhooks` | `/webhooks` | `AuthModeNone`, **no** body-logging, raw-body reader |

`Scopes` on the admin group lists **only** `admin`. `CheckUserScopes`
(`internal/api/middleware/auth.go:236`) is OR semantics, so adding `"app"` alongside would
admit every authenticated user.

## Interfaces & Contracts

### Public API

```
GET    /api/v1/events?category=&page=&perPage=       list, published only
GET    /api/v1/events/featured                        the single featured event
GET    /api/v1/events/{slug}                          detail incl. tiers + derived status
GET    /api/v1/resources?q=&category=&page=&perPage=  list + full-text search
GET    /api/v1/resources/{slug}
GET    /api/v1/faqs?category=
POST   /api/v1/contact-messages                       202, rate-limited
POST   /api/v1/membership/applications                202 ALWAYS, non-disclosing
GET    /api/v1/membership/invitations/{token}         validity probe only, no PII echoed
POST   /api/v1/membership/invitations/{token}/accept  {password} → creates user → session
POST   /api/v1/donations/checkout                     → authorization_url
POST   /api/v1/events/{slug}/register                 free RSVP, or → authorization_url
POST   /api/v1/checkout                               merch; {variantId, quantity}[] only
GET    /api/v1/orders/{reference}                 guest token in X-Order-Token HEADER
POST   /webhooks/paystack                             signature-verified, idempotent
GET    /api/v1/payments/{reference}/verify       token-scoped; server-to-server verify
```

### Admin API (all under `admin` scope)

```
POST|PATCH|DELETE  /api/v1/admin/events[/{id}]        + /publish, /archive, /feature
POST|PATCH|DELETE  /api/v1/admin/events/{id}/tiers[/{tierId}]
POST|PATCH|DELETE  /api/v1/admin/resources[/{id}]
POST|PATCH|DELETE  /api/v1/admin/categories[/{id}]
POST|PATCH|DELETE  /api/v1/admin/faqs[/{id}]
POST|PATCH|DELETE  /api/v1/admin/products[/{id}]      + /variants
GET                /api/v1/admin/contact-messages     + PATCH /{id} (status)
GET                /api/v1/admin/applications         + POST /{id}/approve|reject   [AuthModeSecure]
GET                /api/v1/admin/orders               + POST /{id}/pack|ship|deliver|cancel
POST               /api/v1/admin/payments/{id}/refund                                [AuthModeSecure]
GET                /api/v1/admin/webhook-events?unprocessed=true
POST               /api/v1/admin/media/presign        + POST /media/{id}/complete
```

### Go interfaces

```go
// internal/payment/provider.go
type Provider interface {
    Name() string
    InitializeTransaction(ctx context.Context, req InitializeRequest) (InitializeResult, error)
    VerifyTransaction(ctx context.Context, reference string) (TransactionResult, error)
    VerifyWebhookSignature(rawBody []byte, signature string) bool
    ParseWebhook(rawBody []byte) (WebhookEvent, error)
    CreatePlan(ctx context.Context, req PlanRequest) (PlanResult, error)
    DisableSubscription(ctx context.Context, code, emailToken string) error
    RefundTransaction(ctx context.Context, req RefundRequest) (RefundResult, error)
}

// internal/storage/storage.go
type Storage interface {
    PresignPut(ctx context.Context, key string, contentType string, ttl time.Duration) (string, error)
    Head(ctx context.Context, key string) (ObjectInfo, error)   // TRUE size + type
    Move(ctx context.Context, from, to string) error            // quarantine → public
    Delete(ctx context.Context, key string) error
    PublicURL(key string) string
}

// internal/data/local/transitions.go — the concurrency guard for ALL commerce.
// Returns ErrConflictInvalidStateTransition when RowsAffected != 1.
func transition(ctx context.Context, exec boil.ContextExecutor,
    table, idColumn, id string, from []string, to string, extra map[string]any) error
```

`GalleryItem` renders to the front end's existing `ResourceItem` shape verbatim, so
`/events` and `/resources` need no front-end type changes:

```go
type GalleryItem struct {
    ID, Category, Title, Description, ImageURL string
    Date   time.Time
    Href   null.String
    Status null.String  // events only, DERIVED from tier aggregates, never stored
}
```

## Data

Nineteen tables across eight migrations. Conventions forced or shaped by the build system:

- **Every state machine is a Postgres enum.** Not because the zero-value gate forbids the
  alternative in principle, but because `scripts/sql/default_zero_values.sql:45-52` inspects only
  the boolean, char/text, integer and numeric `data_type` families, so `status text NOT NULL
  DEFAULT 'pending'` is rejected while the enum equivalent is never examined. Note the honest
  consequence: that gate exists *because* `boil.Infer()` skips Go zero values and lets a DB
  default win silently, and sqlboiler maps enums to `string` (zero value `""`), so an enum
  default reintroduces the same footgun where the check cannot see it. **Services therefore set
  status explicitly on insert and never rely on the column default.**
- **No `NOT NULL DEFAULT <non-zero>` on text/bool/int/numeric.** `sort_order integer NOT NULL`
  carries no default.
- **Every FK gets an explicit index** (`sql-check-structure-fk-missing-index`).
- **Every FK to `users` is `ON DELETE SET NULL`** — `DeleteUserAccount`
  (`internal/auth/service.go:544`) is a bare `DeleteAll` on `users` whose own comment says
  "delete the user and all related data" (line 572), relying entirely on cascades. A CASCADE
  here would let an erasure request destroy financial records.
- **`IS NULL` and `NULLS FIRST/LAST` are banned in migrations** until the Phase 0 narrowing of
  `Makefile:235`. Both were verified empirically against the real grep pipeline.

### Migration inventory

| Migration | Tables | Enums |
|---|---|---|
| `...-add-admin-user-scope` | — | `user_scope` (recreated with `admin`) |
| `...-create-content-enums-and-categories` | `categories`, `media_assets` | `category_scope`, `media_status` |
| `...-create-events` | `events` | — |
| `...-create-resources` | `resources` | `resource_kind` |
| `...-create-contact-and-membership` | `contact_messages`, `faqs`, `membership_applications` | `contact_subject`, `contact_message_status`, `application_status` |
| `...-create-merch` | `products`, `product_variants`, `orders`, `order_items` | `currency_code`, `order_status`, `order_kind` |
| `...-create-payments` | `payments`, `payment_refunds`, `payment_webhook_events`, `donations`, `donation_tiers`, `donation_subscriptions` | `payment_provider`, `payment_status`, `donation_kind`, `donation_status` |
| `...-create-event-ticketing` | `event_ticket_tiers`, `event_registrations` | `registration_status` |
| `...-create-admin-audit-log` | `admin_audit_log` | — |

### How the four money flows share one spine

A payment has exactly two possible subjects, and that is sufficient for all four flows:

| Flow | Subject | Mechanism |
|---|---|---|
| Merch | `orders` with `kind = 'merch'` | `order_items.product_variant_id` |
| **Paid tickets** | `orders` with `kind = 'ticket'` | `order_items.ticket_tier_id`; `event_registrations.order_id` links back |
| One-off donation | `donations` with `kind = 'one_off'` | one payment |
| Recurring donation | `donations` with `kind = 'recurring'` | `donation_subscriptions` row; **many** payments over its life |

A ticket purchase is therefore an order, which is why `payments_exactly_one_subject_check` needs
only two FKs. `order_items` carries the mirrored constraint:

```sql
CONSTRAINT order_items_exactly_one_subject_check
  CHECK ((product_variant_id IS NOT NULL)::integer + (ticket_tier_id IS NOT NULL)::integer = 1)
```

Recurring donations are the reason donations are *not* modelled as orders: one subscription
yields many charges over years, with no order per charge.

### Orders carry customer and shipping data

Checkout accepts customer identity and a delivery address; only prices are refused from the
client. `orders` snapshots them (`email`, `full_name`, `phone`, `shipping_line1/line2/city/
state/country/postcode`) so a later profile edit cannot rewrite a past receipt, and so guest
checkout needs no account. `email` is `NOT NULL`; `user_id` is nullable. Ticket orders skip the
address columns; merch orders require them, validated in the service rather than by a CHECK,
because the requirement is conditional on `kind`.

### Load-bearing constraints

```sql
-- Exactly one featured event, as a database invariant rather than an admin convention.
CREATE UNIQUE INDEX idx_events_single_featured ON events (is_featured)
  WHERE (is_featured AND published_at IS NOT NULL);

-- Totals cannot drift: an arithmetic bug becomes a failed INSERT, not an undercharge.
CONSTRAINT orders_total_sum_check
  CHECK (total_kobo = subtotal_kobo + shipping_kobo + tax_kobo - discount_kobo)

-- A payment belongs to exactly one subject. Not (payable_type, payable_id): that gives up
-- referential integrity and sqlboiler relations.
CONSTRAINT payments_exactly_one_subject_check
  CHECK ((order_id IS NOT NULL)::integer + (donation_id IS NOT NULL)::integer = 1)

-- Counters: sold MAY exceed total (a captured late payment is never rejected), but reserved
-- may NOT go negative. Both halves matter -- see the hold-ownership rule below, which is what
-- makes the second half satisfiable.
CONSTRAINT event_ticket_tiers_counters_check
  CHECK (quantity_reserved >= 0 AND quantity_sold >= 0 AND quantity_total >= 0)

-- Webhook de-duplication. Key is the provider's own event identity, NOT a hash of the body:
-- a body digest changes with any re-serialisation or recomputed field, so it can only ever
-- MISS duplicates, never catch more.
CONSTRAINT payment_webhook_events_idempotency_key UNIQUE (provider, idempotency_key)

-- PII cannot be inserted without an expiry.
purge_after timestamptz NOT NULL   -- on contact_messages and membership_applications

-- Full-text search. The two-argument to_tsvector is mandatory: the one-arg form is STABLE
-- (it reads default_text_search_config) and Postgres rejects it in a generated column.
search_tsv tsvector GENERATED ALWAYS AS (
    setweight(to_tsvector('english', COALESCE(title,   '')), 'A') ||
    setweight(to_tsvector('english', COALESCE(summary, '')), 'B') ||
    setweight(to_tsvector('english', COALESCE(author,  '')), 'C') ||
    setweight(to_tsvector('english', COALESCE(body,    '')), 'D')) STORED
```

`resources.search_tsv` must be blacklisted in `sqlboiler.toml`, or `boil.Infer()` includes it in
every INSERT and Postgres answers `column "search_tsv" can only be updated to DEFAULT`.

### Hold ownership — the rule that makes the late webhook safe

`payments.hold_released_at timestamptz` records that this payment's reservation has been given
back. **Exactly one actor may release a hold, and it claims that right conditionally:**

```sql
UPDATE payments SET hold_released_at = now()
 WHERE id = $1 AND hold_released_at IS NULL
```

`RowsAffected == 1` means *you* won the claim and must decrement `quantity_reserved`;
`0` means someone else already released it and you must not. Two consequences:

- **Lazy expiry** wins the claim for an abandoned checkout → `reserved -= n`.
- **A late webhook** loses the claim (expiry got there first) → it adds `sold += n` and does
  **not** touch `reserved`, stamping `oversold_at` on the registration or order.

Without this rule the late webhook decrements a hold that no longer exists, drives
`quantity_reserved` negative, violates the counters CHECK, aborts the transaction and returns
non-2xx — money captured, nothing recorded, and Paystack retrying for 72 hours. That is the
precise failure the "never reject a captured payment" criterion in `01-brief.md` forbids.

### Transaction isolation

All commerce transactions run at **READ COMMITTED**, which is what `db.WithTransaction`
(`internal/util/db/db.go:16`) yields by passing `nil` TxOptions. This is load-bearing, not
incidental: the single-statement reservation is race-free *because* READ COMMITTED makes a
blocked `UPDATE` re-evaluate its `WHERE` against the newly committed row version and report
`RowsAffected = 0` when the predicate no longer holds. At REPEATABLE READ the same statement
raises a 40001 serialization failure instead, and the `RowsAffected != 1 → 409` contract would
silently become a 500. **Commerce services must not use `WithConfiguredTransaction` to raise the
isolation level**, and a test asserts the reservation returns 409 rather than erroring.

### Secrets at rest

| Value | Treatment |
|---|---|
| Membership invitation token | SHA-256 hash only, per S-018/S-024 |
| Guest order-access token | SHA-256 hash only; sent in a header, never a query string |
| Paystack `email_token` | **Reversibly encrypted**, not hashed — it must be replayed to Paystack to cancel a subscription, so a one-way hash is not an option. Key from `SERVER_PAYMENT_SECRET_ENCRYPTION_KEY`. This is the one credential in the system that cannot follow the hash-at-rest rule, and the deviation is deliberate |
| Paystack secret key | Env only, never persisted |

### State machines

```
order_status:    pending ──► paid ──► packed ──► shipped ──► delivered
                    ├──► payment_failed ──► pending      (retry)
                    ├──► expired          [terminal, hold released]
                    └──► cancelled        [terminal, restocks]

payment_status:  pending ──► processing ──► succeeded ──► partially_refunded ──► refunded
                    │            └──► failed
                    ├──► expired | abandoned
                    └──► amount_mismatch  [terminal, ALERT, never fulfils]

registration_status: reserved ──► confirmed ──► cancelled | refunded
application_status:  pending ──► approved ──► accepted
                        └──► rejected | withdrawn ;  approved ──► expired
donation_status:     pending ──► active ──► completed
                        ├──► failed
                        └──► (recurring only) past_due ──► active | cancelled
```

Refund state lives on `payment_status` **only**. It was previously duplicated onto
`order_status`; two copies of one fact that must be kept in sync is a bug generator, and the
payment is the source of truth. An order's refund state is derived by joining its payments.

`amount_mismatch` is a persisted state, not an error return: the discrepancy must survive for
investigation, and returning 500 would lose it while making Paystack retry for 72 hours.

## UX / Flows

### Membership (the flow that could not reuse existing auth)

```
visitor  POST /membership/applications          → 202 ALWAYS (new, duplicate or rejected alike)
                                                  the EMAIL branches; the RESPONSE never does
admin    POST /admin/applications/{id}/approve  → mints invitation token, stores only SHA-256
                                                  of it on the APPLICATION row; emails the link
invitee  POST /membership/invitations/{t}/accept {password}
                                                → password.Validate + hashing.HashPassword
                                                → CREATES the user here, first time
                                                → authenticateUser → session
```

No user row exists until acceptance, so a rejected application leaves no orphan and the invite
link is not a bearer credential. Reusing `CompleteRegister` (`internal/auth/service.go:588`)
would have produced a user with a `NULL` password whom `Login` (line 283) could never
authenticate and `UpdatePassword` (line 58) would reject with `ErrForbiddenNotLocalUser`.

**This endpoint carries a password in its body** and therefore needs a `RequestBodyLogReplacer`
on its route group, exactly as `/api/v1/auth` already has — the default replacer
(`internal/api/middleware/logger.go:63`) returns the body unmodified.

### Checkout (identical for merch and tickets)

```
1. client sends {variantId|tierId, quantity}[] + customer + shipping address — NEVER a price
2. SORT the lines by primary key                      ← deadlock avoidance, see below
3. expire stale holds on the touched rows (lazy; there is no scheduler), claiming
   each hold via UPDATE payments SET hold_released_at = now() WHERE ... IS NULL
4. reserve, one statement per line, in the sorted order:
     UPDATE ... SET reserved = reserved + $2
      WHERE id = $1 AND reserved + sold + $2 <= total
   RowsAffected != 1 → does the row exist?  no → 404   yes → collect into a structured 409
5. server computes subtotal/shipping/tax/total from its own rows
6. create order + payment (expires_at = now + hold TTL), call Paystack initialize
7. return authorization_url
8. webhook OR verify-on-return → the SAME idempotent transition → claim the hold, then
   sold += n, and reserved -= n ONLY if this actor won the claim
```

**Step 2 is not cosmetic.** Without a deterministic lock order, two carts containing variants A
and B in opposite orders deadlock (40P01) inside one transaction. Sorting by primary key gives
every transaction the same acquisition order, which makes the deadlock unreachable. The
acceptance load test must therefore use a **multi-line cart**, not the single row originally
specified in `01-brief.md`, or it cannot detect this class of bug at all.

Step 4 collects failures rather than aborting on the first, so the client-side cart can be told
*which* lines failed and why. It also distinguishes a missing variant (404) from a genuinely
sold-out one (409), which a bare `RowsAffected = 0` would conflate.

### Webhook

```
read RAW body ──► HMAC-SHA512(raw, secretKey) vs x-paystack-signature via hmac.Equal
                    │ mismatch → 401, log NOTHING from the body
                    ▼
ONE transaction:
  INSERT payment_webhook_events (provider, idempotency_key = <event_type>:<data.id>)
      │ 23505 → duplicate → mark the EXISTING row processed if it is not → COMMIT → 200
      ▼
  transition payment (conditional UPDATE ... WHERE status = ANY(...))
      │ RowsAffected 0 → already handled → mark processed → COMMIT → 200
      ▼
  claim the hold, adjust counters with the SAME exec
      ▼
  set processed_at ──► COMMIT ──► 200
```

Three properties this shape buys:

- **The dedup insert shares the processing transaction.** Committing it separately would mean a
  crash between insert and processing leaves the event permanently deduped away and never
  retried — money taken, nothing recorded, with the retry mechanism disabled by our own guard.
- **`idempotency_key` is `<event_type>:<data.id>`**, the provider's own event identity. An
  earlier draft appended `sha256(rawBody)`; that is strictly worse, because any re-serialisation
  or recomputed field yields a fresh key and a second pass. A body digest can only miss
  duplicates, never catch more.
- **Every path sets `processed_at`, including the duplicate path.** `processed_at IS NULL` is
  the operational alarm for "money taken, nothing recorded"; leaving correctly-handled
  duplicates in that state would bury the one signal that catches a genuine failure.

Every effect is an idempotent state *transition*, never a bare increment.

A note on the pattern this guards against: `s.db` used inside a `WithTransaction`
closure escapes the rollback. In `internal/auth/service.go:596` that was a real defect and is
fixed. In `internal/data/local/service_push.go` the same shape turned out to be **intentional** —
a device presenting an unknown old token still has a valid new one, so the new token is committed
and the missing old one reported afterwards. That file has been split into two explicit
transactions so the intent is stated rather than inferred from which connection a call happens to
use. The lesson for the webhook handler is unchanged: partial commit must be a decision someone
wrote down, never a side effect of an executor choice.

### Retention

`purge_after` is enforced as a `NOT NULL` column, but a constraint that nothing acts on is
decorative. **Owner: `app db purge-expired`**, a cobra subcommand beside `cmd/db/seed.go`, on a
Render cron. Deletion is hard, not soft — soft-deleted PII is still PII under the NDPA 2023.
Lazy purging on a request path is deliberately *not* used here: unlike a reservation hold,
nothing about serving a request depends on expired PII being gone, so there is no self-healing
path and the cron is the only mechanism.

## Error Handling

New `types.PublicHTTPErrorType` values in `api/definitions/errors.yml`, surfaced as package-level
vars in `internal/api/httperrors/`:

| Error | Status | Type |
|---|---|---|
| `ErrNotFoundVariant` | 404 | `VARIANT_NOT_FOUND` |
| `ErrConflictInsufficientStock` | 409 | `INSUFFICIENT_STOCK` |
| `ErrConflictInsufficientCapacity` | 409 | `INSUFFICIENT_CAPACITY` |
| `ErrConflictCartChanged` | 409 | `CART_CHANGED` (carries per-line detail) |
| `ErrConflictInvalidStateTransition` | 409 | `INVALID_STATE_TRANSITION` |
| `ErrConflictSlugTaken` | 409 | `SLUG_TAKEN` |
| `ErrForbiddenSlugImmutable` | 403 | `SLUG_IMMUTABLE` |
| `ErrNotFoundInvitationInvalid` | 404 | `INVITATION_INVALID` |
| `ErrBadRequestUnsupportedMediaType` | 400 | `UNSUPPORTED_MEDIA_TYPE` |
| `ErrUnprocessablePaymentAmountMismatch` | 422 | `PAYMENT_AMOUNT_MISMATCH` |

Rules:
- **Never reject a captured payment.** Capacity breaches discovered after capture are recorded
  (`oversold_at`) and surfaced to an admin, never refused. The hold-ownership rule above is what
  makes this actually achievable rather than merely stated.
- **Never leak existence.** `/membership/applications` is always 202; `/register` is made
  non-disclosing in the same pass, closing S-020.
- **Degrade search, don't 500.** A `to_tsquery` syntax failure returns empty results; terms are
  sanitised to `[\p{L}\p{N}_-]` first. Unsanitised, `SearchStringToTSQuery` turns `foo)` into a
  fuzzable 500 on a public GET.
- **Guest order access is by header, not query string.** `req.URL.String()` is logged
  unconditionally at `internal/api/middleware/logger.go:200` — it is *not* gated by
  `SERVER_LOGGER_LOG_REQUEST_QUERY`, which defaults false — so a token in a query string lands
  in the structured request log verbatim, plus proxy logs and `Referer` headers. The same
  applies to `GET /api/v1/payments/{reference}/verify`, which is therefore token-scoped too.
- **Webhook failures return non-2xx deliberately** so Paystack retries.

## Critic Review

Adversarial gate review run in a fresh context per `core/_gate-review.md`. Verdicts and
resolutions:

| # | Check | Initial | Resolution | Final |
|---|---|---|---|---|
| 1 | Grounding | WARN | Citations corrected (`DeleteUserAccount` is at :544/:572, not :665); Paystack `data.id` claim corrected | PASS |
| 2 | Internal consistency | **BLOCK** | Late webhook drove `quantity_reserved` negative against the counters CHECK. Resolved by the hold-ownership rule | PASS |
| 3 | Unstated assumptions | WARN | READ COMMITTED, lock ordering, `Storage.Move` non-atomicity and the retention owner now stated | PASS |
| 4 | Completeness | WARN | Table/migration arithmetic corrected (19 tables, 8 migrations) | PASS |
| 5 | Component interfaces | WARN | `Storage.GetRange` cut; `admin_audit_log` given an owner and a read endpoint | PASS |
| 6 | Failure modes | **BLOCK** | Cross-line deadlock unhandled; load test too narrow to detect it. Sorted lock order added; test widened to a multi-line cart | PASS |
| 7 | Satisfies the brief | **BLOCK** | Paid tickets had no payment subject; shipping address and guest email uncaptured; recurring donations had no subscription table or cancel path. All three added | PASS |
| 8 | Simplest design | WARN | Cut `payment_purpose` (derivable from the FK), `registration_status.checked_in`, `order_status.partially_refunded`, `Storage.GetRange` | PASS |
| 9 | Authn/authz per endpoint | WARN | `/payments/{reference}/verify` token-scoped; per-route `AuthModeSecure` wiring specified | PASS |
| 10 | Trust boundaries | WARN | Webhook raw-body boundary and presign quarantine→verify→promote already specified; dedup transaction boundary now explicit | PASS |
| 11 | Sensitive data vs the audit | WARN | Guest token moved out of the query string and hashed; invitation-accept body redaction specified; `email_token` encryption deviation documented | PASS |

Two findings accepted rather than fixed:
- **`category_scope` is a discriminator on a shared table**, which resembles the polymorphism
  rejected for content. Kept deliberately: categories have *identical* columns in both scopes,
  where events and resources do not — the rejection was about divergent columns and unenforceable
  `NOT NULL`, neither of which applies here.
- **The in-memory rate limiter multiplies per instance** on multi-instance Render. Recorded as a
  scaling constraint for `05-verify.md` rather than solved now; Render runs one instance today.

## Sources

- Layering, Route naming regex, binding and error conventions — `internal/api/handlers/push/put_update_push_token.go`, `internal/util/http.go`, `internal/api/httperrors/`, `scripts/internal/handlers/gen.go` — 2026-09-09
- Zero-value gate inspects only boolean/char/text/integer/numeric `data_type`s, never `USER-DEFINED` — `scripts/sql/default_zero_values.sql:45-52` — 2026-09-09
- `IS NULL` and `NULLS ` fail the migration NULL check; `IS NOT NULL` passes — `Makefile:235-238`, verified empirically against the real GNU-grep pipeline — 2026-09-10
- Every FK needs an index — `scripts/sql/fk_missing_index.sql` — 2026-09-09
- `CheckUserScopes` OR semantics; empty list allows — `internal/api/middleware/auth.go:228-246` — 2026-09-09
- `AuthModeSecure` requires recent re-authentication — `internal/api/middleware/auth.go:44-46` — 2026-09-09
- `CompleteRegister` never accepts a password — `internal/auth/service.go:588-643`; `UpdatePassword` at :58 and `Login` at :283 both require a hash — 2026-09-10
- `DeleteUserAccount` is a bare `DeleteAll` relying on cascades — `internal/auth/service.go:544`, delete at :572 — 2026-09-10
- `db.WithTransaction` passes nil TxOptions, yielding the server default READ COMMITTED — `internal/util/db/db.go:16-21` — 2026-09-10
- `req.URL.String()` is logged unconditionally, not gated by `LogRequestQuery` (default false) — `internal/api/middleware/logger.go:200`, `internal/config/server_config.go:326` — 2026-09-10
- Default `RequestBodyLogReplacer` returns the body unmodified — `internal/api/middleware/logger.go:63` — 2026-09-10
- `SearchStringToTSQuery` strips quotes but not tsquery operators — `internal/util/db/ts_vector.go` — 2026-09-09
- `mimetype` already a dependency for content sniffing — `internal/util/http.go:176` — 2026-09-09
- sqlboiler maps a Postgres enum column to a plain Go `string` — `internal/models/push_tokens.go:28` against `migrations/20200529112509-create-push-token.sql:10` — 2026-09-10
- Paystack: HMAC-SHA512 hex over raw body in `x-paystack-signature`; no timestamp, so no replay window; dedupe on event + `data.id`/`reference`; 30s timeout; retries 3min×4 then hourly to 72h; source IPs 52.31.139.75 / 52.49.173.169 / 52.214.14.220 — https://hookdeck.com/webhooks/platforms/guide-to-paystack-webhooks-features-and-best-practices — 2026-09-10
- Paystack subscriptions require a Plan; cancellation needs `subscription_code` + `email_token`, returned once at creation, so it must be stored replayably — https://github.com/PaystackHQ/documentation/blob/master/recurring-payments/subscriptions.md — 2026-09-10
- PostgreSQL generated columns require IMMUTABLE expressions, excluding one-arg `to_tsvector` — https://www.postgresql.org/docs/current/ddl-generated-columns.html — 2026-09-10
- PostgreSQL READ COMMITTED re-evaluates an UPDATE's WHERE against the newly committed row after a lock wait — https://www.postgresql.org/docs/current/transaction-iso.html#XACT-READ-COMMITTED — 2026-09-10
