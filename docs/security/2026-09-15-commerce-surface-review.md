# Security review — commerce surface

2026-09-15. Adversarial review of the payments, ticketing, merch, donations,
media and membership code added in Phases 2-10, followed by independent
reproduction of every finding against the running code.

Numbering continues from `2026-09-08-security-audit.md`, which ends at S-028.
Twelve findings were raised and all twelve were confirmed; eleven are fixed and
one (S-041) is deferred with a flip condition.

Each fix is covered by a test that **fails when the fix is reverted** — verified
by actually reverting it. `.workflow/05-verify.md` names the test for each.

## Summary

| ID | Severity | Status |
|---|---|---|
| S-029 | High | fixed |
| S-030 | High | fixed |
| S-031 | High | fixed |
| S-032 | Medium | fixed |
| S-033 | High | fixed |
| S-034 | High | fixed |
| S-035 | High | fixed |
| S-036 | Medium | fixed |
| S-037 | High | fixed |
| S-038 | Medium | fixed |
| S-039 | Medium | fixed |
| S-040 | Low | fixed |
| S-041 | Low | deferred |

## Findings

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

## Themes

Three of these share a root cause worth naming, because it will recur.

**A comment asserting that something is handled elsewhere is not a handoff.**
S-035 hid behind "stock and seat counters are adjusted by the phases that
introduce them". Nothing tracked that claim, and it read on review as a decision
already taken rather than work not yet done. A `TODO` would have been found; a
confident sentence was not.

**Documenting a hazard does not prevent it.** S-031 is precisely the bug class
called out in the plan, fixed in two pre-existing places, and recorded in
`.workflow/memory.md` — and it was then written fresh four phases later. What
catches it is the test that pins the connection pool to one connection and makes
the deadlock deterministic, not the note.

**Consistency is not a security argument.** S-034 defaulted the payment mock on
because push and storage do. Those degrade to a no-op; a payment mock degrades to
a signature oracle. The right question is what the mock does when reached by
mistake, not what its neighbours default to.
