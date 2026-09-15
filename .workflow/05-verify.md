# 05 — Verify

Phase 10 of the Cloudadore backend build. The domain layer is complete: content,
membership, merch, ticketing, one-off and recurring donations, refunds and the
admin operational surface. This artifact records what was tested, what was
broken, and what remains.

Findings are numbered from **S-029**, continuing the namespace of
`docs/security/2026-09-08-security-audit.md`, which ends at S-028.

## Test Coverage

- **451 test functions across 36 packages**, all green under `go test -race ./...`
  in the dev container. 0 data races.
- `make build` clean: `sql swagger go-generate go-generate-handlers go-format
  go-build lint`. **golangci-lint reports 0 issues** with `mnd`, `varnamelen`,
  `wrapcheck` and `gocyclo`(30) enabled.
- 15 packages exercise real PostgreSQL through IntegreSQL. Handler tests drive
  the actual router, so middleware, scope checks and binding are in the path.

Concurrency is tested by racing goroutines against real rows, not by reasoning:

| Property | Test | Method |
|---|---|---|
| Ticket tiers never oversell | `TestConcurrentRegistrationsNeverOversell` | 10 buyers, 3 seats |
| Stock never oversells | `TestConcurrentCheckoutsNeverOversell` | 10 carts, 3 units |
| Cross-line ordering cannot deadlock | `TestOppositeOrderCartsDoNotDeadlock` | 6 pairs, both directions |
| Webhook replay is idempotent | `TestWebhookReplayIsIdempotent` | 5 identical deliveries |
| Settlement sells units once | `TestWebhookReplayDoesNotSellTwice` | 3 deliveries, 1 sale |
| Subscription effects need one connection | `TestSubscriptionWebhookEffectsRunInTheCallerTransaction` | pool pinned to 1 |

**Every fix below was mutation-tested**: the fix was reverted, the new test was
observed to fail, and the fix restored. A test that passes against the broken
code proves nothing, and two drafts here did exactly that before being rewritten
(see *Bugs Found*, S-040).

## Bugs Found

Twelve issues were raised by an adversarial review of the completed commerce
surface. All twelve were independently reproduced against the code before any
change was made; none were taken on the reviewer's word.

Three were serious enough to describe in full:

**S-035 — paid orders never became sales.** `settleSucceededTransaction` moved
the order to `paid` and stopped, under a comment asserting that "stock and seat
counters are adjusted by the phases that introduce them". No phase ever did.
Reserved units were therefore never converted to sold and the hold was never
released, so a paid hoodie stayed reserved forever; a later refund then
decremented a `stock_sold` that had never been incremented, and the units were
withdrawn from sale permanently. A paid ticket registration stayed `reserved` and
the attendee never received a ticket. The comment is what made this invisible —
it read as a completed decision rather than an unbuilt one.

**S-031 — subscription webhook effects escaped their transaction.**
`RecordSubscriptionCreated`, `MarkSubscriptionDisabled` and
`MarkSubscriptionPastDue` were called without the outer `exec` and used `s.db`
fourteen times; `MarkSubscriptionDisabled` additionally opened a *nested*
`db.WithTransaction`. A nested transaction needs a second pooled connection while
the outer holds the first, so with `DB_MAX_OPEN_CONNS=10` a busy moment deadlocks
the webhook handler. This is the exact bug class flagged in `02-plan.md` Phase 0,
fixed in two pre-existing places, and written into `.workflow/memory.md` — and
then committed anyway, four phases later. Documenting a hazard does not prevent
it; only a test does.

**S-034 — the mock payment provider was the default.** `SERVER_PAYMENT_USE_MOCK`
defaulted to `true` and `SERVER_PAYMENT_MOCK_SECRET_KEY` to the committed literal
`"mock-payment-secret"`, mirroring how push and storage behave. Those degrade to
a no-op when mocked; this one degrades to a forgery oracle, because the mock
verifies webhook signatures with a key that is in the repository. Any deployment
not going through `render.yaml` would let an unauthenticated caller sign their
own `charge.success` and be sent free merchandise. Consistency with push and
storage was the wrong instinct.

## Fixes Applied

| ID | Fix | Proven by |
|---|---|---|
| S-029, S-030 | `addRefundedAmount` — one `UPDATE … WHERE amount_refunded + $2 <= amount_kobo RETURNING`, replacing both a stale-read accumulate and an outright assign | `TestWebhookRefundsAccumulate`, `TestWebhookRefundBeyondTheChargeIsRefused` |
| S-031 | `exec` threaded through all three subscription effects; nested transaction removed | `TestSubscriptionWebhookEffectsRunInTheCallerTransaction` (deadlocks in 15s when reverted) |
| S-032 | An event with neither id nor reference is refused, not deduped against every other event of its type | `ErrUnidentifiableEvent` |
| S-033 | Invitation token moved from the URL path into the POST body | `community_test.go` (6 call sites) |
| S-034 | Mock defaults off; refuses to construct without an explicit key | `TestPaymentProviderIsNotMockedByDefault` + 3 |
| S-035 | `fulfilOrderReservations` — converts reserved→sold for variants and tiers and confirms registrations, gated on `claimHoldRelease` | `TestWebhookConvertsReservedStockIntoSold`, `TestWebhookConfirmsPaidRegistrations`, `TestWebhookReplayDoesNotSellTwice` |
| S-036 | `maxItems: 50` on cart lines | `TestOversizedCartIsRejected` |
| S-037 | `CopyObject` now sets `ReplaceMetadata` and writes the **sniffed** content type | `TestPromotionRewritesTheStoredContentType` |
| S-038 | An amount-less refund event means the remainder, not the full charge | `TestWebhookRefundWithoutAmountRefundsTheRemainder` |
| S-039 | Commerce paths added to `sensitiveBodyPaths` | comment and list now agree |
| S-040 | Payment references use RFC 4648 base32; the post-encoding `!`→`7` fold is gone | `TestReferenceAlphabetNeedsNoSubstitution` |

Two test drafts passed against the broken code and were rewritten:

- The first S-040 test asserted the *alphabet* had no duplicate characters. It
  did — `!` is distinct from `7`. The collision came from the substitution
  applied *after* encoding. The property that matters is that the alphabet is
  already alphanumeric, so no substitution is needed at all.
- The first S-029 cap test used a single oversized refund, which a per-event
  bound also rejects. It now uses two refunds of 8000 against a 10000 charge —
  each legal alone — and asserts the refund **row** count, because the stored
  amount alone cannot distinguish "refused" from "assigned over the first".

## Residual Risks

- **`Str("url", req.URL.String())` in the request logger bypasses
  `RequestQueryLogReplacer`.** The config offers query redaction that the URL
  field silently defeats. No route currently carries a secret in its query
  string, and S-033 removed the one that carried one in its path, so this is
  latent rather than live. Recorded as S-041 below.
- **The rate limiter is in-memory and per-process.** Two Render instances double
  the effective limit per attacker. Unchanged from the earlier audit.
- **The password pepper remains undecided.** Cheapest before real member data
  exists, which this project is about to create. Carried to GTM as an explicit
  ask rather than silently dropped.
- **`quantity_sold` may exceed `quantity_total` by design.** A webhook arriving
  after a hold expired grants the seat, stamps `oversold_at`, and leaves an
  admin to resolve it. Never rejecting a captured payment is the deliberate
  trade; the alternative is taking money for nothing.
- **Shipping is a flat zone row and VAT is 0 basis points.** Both are
  configuration, not code, and both still need a business decision. The schema
  carries `tax_kobo` and `weight_grams` so neither choice requires a migration.

## Acceptance

- `make build` green, including `golangci-lint` at 0 issues — PASS
- `go test -race ./...` green across 36 packages, 0 data races — PASS
- Ticket tiers never oversell under 10 concurrent buyers on 3 seats — PASS
- Product variants never oversell under 10 concurrent carts on 3 units — PASS
- Carts sharing variants in opposite orders do not deadlock — PASS
- A replayed webhook delivery changes nothing and returns 200 — PASS
- A settled order converts reserved stock to sold and releases its hold — PASS
- A paid ticket registration is confirmed when the money arrives — PASS
- Refunds accumulate and are capped against the running total — PASS
- Subscription webhook effects complete on a single pooled connection — PASS
- The admin surface refuses a caller without the `admin` scope — PASS
- Refunds require recent authentication (`AuthModeSecure`) — PASS
- Media promotion rewrites the stored content type to the sniffed one — PASS
- Every fix above fails its test when reverted — PASS
- `/members/apply` and `/register` do not disclose whether an address exists — PASS
- Generated code is committed and the tree is diff-clean after `make build` — PASS

## Security

S-029 — provider-reported refunds overwrote the running total instead of accumulating — High — internal/data/local/service_payment.go:456 — a ₦100 charge refunded twice at ₦40 recorded ₦40 refunded, leaving ₦60 still apparently refundable after ₦80 had gone back; the admin path accumulated while the webhook path assigned, so the two disagreed about the same column — both paths now share addRefundedAmount, which adds and caps in a single UPDATE — fixed

S-030 — the admin refund path accumulated from a stale read — High — internal/data/local/service_admin_ops.go:263 — paymentRow was loaded before the transaction opened, so two concurrent refunds each read the same amount_refunded_kobo, each wrote their own total, and the larger silently won — the read-then-write is replaced by a conditional UPDATE whose predicate re-evaluates against the newly committed row under READ COMMITTED — fixed

S-031 — subscription webhook effects escaped the webhook transaction and could deadlock the connection pool — High — internal/data/local/service_subscriptions.go — three effects were called without the outer exec and used s.db fourteen times, and MarkSubscriptionDisabled opened a nested transaction needing a second pooled connection while the outer held the first; with DB_MAX_OPEN_CONNS=10 a busy moment blocks the handler indefinitely, and the writes committed independently of the guarded transition that makes delivery idempotent — exec threaded through all three, nested transaction removed, and a test pins the pool to one connection so the deadlock is deterministic rather than load-dependent — fixed

S-032 — a webhook event carrying neither an id nor a reference poisoned de-duplication — Medium — internal/payment/paystack/paystack.go:162 — the idempotency key fell back to the bare event type, so the first such delivery was recorded and every later event of that type was silently discarded as a duplicate, money events included — such an event is now refused with ErrUnidentifiableEvent, which returns non-2xx and lets the provider retry — fixed

S-033 — the membership invitation token travelled in the URL path — High — internal/api/handlers/community/post_accept_invitation.go:15 — the token is a bearer credential that creates an account, and internal/api/middleware/logger.go:200 logs every request URL unconditionally; sensitiveBodyPaths protected the body but not the path, and a URL also reaches proxy and CDN access logs, the Referer header of any outbound link, and browser history, none of which this service controls — the token moved into the POST body and the route is now /api/v1/membership/invitations/accept — fixed

S-034 — the mock payment provider was enabled by default with a committed signing key — High — internal/config/server_config.go:483 — SERVER_PAYMENT_USE_MOCK defaulted to true and MockSecretKey to the literal "mock-payment-secret", so any deployment not going through render.yaml let an unauthenticated caller sign their own charge.success and receive free fulfilment — the default is inverted, the key has no default, and NewPaymentProvider refuses to construct the mock without an explicit one — fixed

S-035 — paid orders never converted reserved stock or seats into sales — High — internal/data/local/service_payment.go:303 — settlement moved the order to paid and stopped, so stock_sold and quantity_sold stayed at zero, holds were never released, paid registrations stayed reserved and never became tickets, and a subsequent refund decremented a sold counter that had never been incremented — permanently withdrawing the units from sale — fulfilOrderReservations now converts both counters and confirms registrations, gated on claimHoldRelease so a late webhook cannot double-release — fixed

S-036 — cart lines were unbounded — Medium — api/definitions/community.yml:467 — minItems was set and maxItems was not; checkout holds one pooled connection and takes a row lock per line for the whole transaction, and the 1MB body limit still admits roughly 17,000 lines, so one anonymous request could occupy a ten-connection pool — maxItems: 50 — fixed

S-037 — the client-chosen Content-Type survived promotion to the public bucket — High — internal/storage/s3/s3.go:117 — PresignedPutObject signs only method, bucket, key and expiry, so the uploading client sets the stored Content-Type freely, and CopyObject without ReplaceMetadata carried it onto the public object; the sniffed type was written to the database but never to the object, so a genuine PNG could be served to every visitor as text/html — Move now takes the sniffed content type and sets ReplaceMetadata — fixed

S-038 — a refund event with no amount was treated as a full refund — Medium — internal/data/local/service_payment.go:428 — the fallback assumed the entire charge, which double-counts against anything already refunded and causes the cap to reject a refund that genuinely happened, losing its record — the fallback is now the remaining unrefunded balance, and an event with nothing left to refund is logged and ignored — fixed

S-039 — the commerce endpoints were absent from the request-body log skipper — Medium — internal/api/router/router.go:36 — the list's own comment claimed the commerce surface was covered and it was not, so with SERVER_LOGGER_LOG_REQUEST_BODY=true a full shipping address, attendee names and donor identities (including donations marked anonymous) were written verbatim to the log aggregator — /api/v1/checkout, /api/v1/donations and /api/v1/events added — fixed

S-040 — the payment reference alphabet folded two values onto one character — Low — internal/data/local/reference.go:22 — a 30-character confusable-free set was padded to 32 with "!" and "!" was rewritten to "7" after encoding, so two distinct 5-bit values rendered identically, discarding entropy and admitting collisions on a column the provider's webhook uses as a key — references now use RFC 4648 base32, which is 32 characters and already alphanumeric, so no substitution is needed — fixed

S-041 — the request logger's url field bypasses the configured query redactor — Low — internal/api/middleware/logger.go:200 — Str("url", req.URL.String()) includes the raw query regardless of LogRequestQuery being false or RequestQueryLogReplacer being set, so the config offers a redaction the url field defeats; no current route carries a secret in its query string, and S-033 removed the only one carrying a credential in its path — deferred(latent, not live; flip condition: the first route that accepts a token, signature or PII in a query parameter)

## Not Covered

- **No live Paystack transaction.** Every money path is verified against the mock
  with hand-crafted HMAC-SHA512 signatures over raw bodies, which exercises the
  real verification code — but no real ₦100 has moved. `02-plan.md` makes this a
  precondition for Phases 6 and 7, and it is unmet pending test keys. Switching
  is configuration only: `SERVER_PAYMENT_USE_MOCK=false` plus
  `SERVER_PAYMENT_SECRET_KEY`.
- **No load test at production scale.** Concurrency is proven at 10 goroutines
  against real rows, which is enough to demonstrate the reservation property but
  not to characterise behaviour under sustained traffic.
- **Email deliverability (SPF/DKIM/DMARC) is unverified.** Receipts and ticket
  confirmations are exactly the mail people report not receiving. Carried to GTM.
- **The retention sweeper is implemented but not scheduled.** `purge_after` is
  `NOT NULL` on both PII tables so nothing can be inserted without an expiry, but
  nothing runs the sweep yet. Carried to GTM as a Render cron.
- **S-022 and S-023 remain deferred** from the earlier audit, both Low and both
  flip-on-advisory. **S-020 is now closed** — `/register` is non-disclosing when
  `SERVER_AUTH_REGISTRATION_REQUIRES_CONFIRMATION=true`, and
  `TestPostRegisterDisclosesWhenConfirmationDisabled` pins the residual gap when
  it is not.
- **No frontend integration run.** The `team-cloudadore` repo still renders
  hardcoded arrays; swapping them for live calls is GTM work.

## Sources

- `internal/data/local/service_payment.go`, `service_subscriptions.go`,
  `service_admin_ops.go`, `service_merch.go`, `service_ticketing.go`,
  `transitions.go` — read directly to confirm each finding before changing it.
- `Makefile:235` (`sql-check-migrations-unnecessary-null`) and
  `scripts/sql/default_zero_values.sql:45` — the two build checks that shape the
  schema; verified by reading, not assumed.
- PostgreSQL READ COMMITTED / EvalPlanQual semantics — the basis for every
  conditional UPDATE here: a blocked writer re-evaluates its WHERE against the
  newly committed row. https://www.postgresql.org/docs/current/transaction-iso.html
- Paystack webhook signing: HMAC-SHA512 hex over the raw body in
  `x-paystack-signature`, no timestamp and no event id, retried every 3 minutes
  ×4 then hourly to 72 hours. https://paystack.com/docs/payments/webhooks/
- MinIO Go `CopyDestOptions.ReplaceMetadata` — without it `CopyObject` preserves
  source metadata. https://pkg.go.dev/github.com/minio/minio-go/v7
- RFC 4648 §6, base32 alphabet. https://www.rfc-editor.org/rfc/rfc4648#section-6
- `docs/security/2026-09-08-security-audit.md` — S-001..S-028, the namespace this
  artifact continues from.
- NDPA 2023 (Nigeria Data Protection Act) — the basis for hard deletion of PII
  and `purge_after NOT NULL`.
