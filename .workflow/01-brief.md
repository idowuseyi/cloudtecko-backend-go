# 01 — Discovery Brief

## Problem

The Cloudadore community site (`prosperomage/team-cloudadore`, Next.js 16 / React 19) is a
complete front end with no back end. Every page renders hardcoded TypeScript arrays; there is
not a single `fetch` in the repository. Two pages are unbuilt stubs — `/events/[slug]` renders
the literal string "individual event cards content page", `/merchstore` renders "coming soon".
The contact form's submit handler is `alert("form submitted")`. Nothing a visitor does is
recorded anywhere.

This repository is the intended back end and is domain-empty: a hardened fork of
`allaboutapps/go-starter` carrying auth, push tokens and health probes, with no business
feature of any kind. Its entire API surface is `/api/v1/auth/*`, `/api/v1/push/token`,
`/-/healthy`, `/-/ready`, `/-/version`, `/swagger.yml` and two `.well-known` files.

The gap is the whole domain layer.

## Users & Context

- **Visitors** — read events and learning resources, filter and search them, submit the contact
  form, apply to join the community, donate, buy merch, register for events.
- **Members** — approved applicants with accounts. Today the only thing an account grants is
  the ability to hold one; member-only privileges are not yet defined.
- **Admins** — create and publish content, review membership applications, fulfil orders, issue
  refunds. There is no admin UI in scope; they drive the API directly.

Context that constrains the work: pricing is in Naira (₦), the audience is primarily Nigerian,
the site's own FAQ tells applicants they join "through our waitlist" and are approved by email,
and the front end already depends on `zustand` — which is what a client-side cart looks like.

## Requirements

1. **Events** — public list with category filter and pagination, detail by slug, one featured
   event, admin CRUD. Times stored as `timestamptz` plus an IANA timezone; virtual or physical.
2. **Resources** — public list with full-text search, category filter, pagination, admin CRUD.
3. **Contact & FAQ** — contact-message intake (name, email, subject enum, message); admin-managed FAQs.
4. **Membership** — public waitlist application, admin approve/reject, invitation email, and an
   accept-with-password step that creates the account.
5. **Merch** — products with size/colour variants, per-variant stock and price, physical
   shipping, order fulfilment states.
6. **Payments** — Paystack, behind an internal `Provider` interface with a mock implementation.
7. **Money flows** — merch orders, one-off donations, recurring donations, paid event tickets.
8. **Admin API** — a new `admin` scope guarding `/api/v1/admin/*`.
9. **Media** — presigned upload to S3-compatible object storage with real MIME and size validation.

Non-functional: `make build` clean (it runs codegen, gofmt, build and a strict golangci-lint,
and CI then diffs the tree); no regression in the security posture the recent audit established;
guest checkout supported; money as integer kobo, never floats.

## Success Criteria

- Every page of the existing front end renders from the API with its hardcoded arrays deleted,
  and looks the same.
- The contact form and the waitlist form both persist and are visible to an admin.
- A donation of ₦100 completes end to end in Paystack test mode, is verified server-side, and is
  idempotent under a replayed webhook.
- A concurrent load test against one ticket tier and one product variant never oversells beyond
  the documented late-webhook case, and never rejects a captured payment.
- `make build` and `make lint` are clean; the database-backed test packages pass in the dev container.
- No open Critical or High security finding at the Verify gate.

## Options Considered

**Content storage — one polymorphic table vs two aggregates.** The front end feeds both events
and resources into one `ResourceGallery` component using one `ResourceItem` type, which argues
for a single `content_items` table with a discriminator. Rejected: the column sets barely
overlap (~8 shared against 8 event-only and 5 resource-only), `start_at` could not be `NOT NULL`,
and the `status: Open|Sold Out|Limited` field is *derived from ticket-tier aggregates* for events
and meaningless for resources — so in one table it is either a column that goes stale on every
sale or a computed column null for half the rows. Component reuse is a front-end fact, not a
storage fact, and no screen renders a mixed list.

**Membership — reuse the existing confirmation flow vs a new accept endpoint.** Reusing
`POST /api/v1/auth/register/{registrationToken}` looked free. Rejected on inspection: that
handler sets `is_active`, deletes the token and issues a session, and never accepts a password,
because `/register` sets the password *before* the token is minted. Wiring approval to it would
create a user row with a `NULL` password and make the invite email a 24-hour bearer credential
that logs in whoever opens it — and the member could never log in afterwards.

**Payment→subject relation.** A `(payable_type, payable_id)` pair gives up referential integrity
and sqlboiler relations. Forcing donations into `orders` breaks outright for recurring donations,
where one subscription yields many charges over years with no order per charge. Chosen: nullable
FKs plus a CHECK that exactly one is set.

**Scope sequencing.** A content-only first slice was offered and declined; the full commerce
surface is in scope. It is therefore phased rather than reduced.

## Chosen Direction

Build the domain layer in eleven dependency-ordered phases inside this repository, following the
service-layer architecture this fork has already grown (handler binds and delegates; service owns
the transaction; dto in, `ToTypes()` out).

Two aggregates for content with a shared `dto.GalleryItem` read projection. Postgres enums for
every state machine — forced by `scripts/sql/default_zero_values.sql`, which rejects non-Go-zero
defaults on text columns but never inspects `USER-DEFINED` types. A single conditional-`UPDATE`
transition helper as the concurrency guard for the entire commerce surface. Reservation of stock
and seats in one statement whose predicate and increment share a row lock, with lazy expiry on
the checkout path rather than a scheduler this repository does not have.

Critical path is Phase 0 (foundations) → 4 (payment core) → 5 (one-off donations); phases 1–2
(content, admin CRUD) deliver visible value in parallel. Merch and ticketing do not start until
a real ₦100 has moved in test mode.

## Sources

- Front-end structure, page contents, hardcoded data arrays and `types/index.ts` — `gh api repos/prosperomage/team-cloudadore/git/trees/main?recursive=1` and `gh api .../contents/<path>` — 2026-09-09
- Front end depends on `zustand` 5.0.12, Next 16.2.1, React 19.2.4 — `package.json` via `gh api` — 2026-09-09
- Site FAQ states members join "through our waitlist" and are approved by email invitation — `app/contact/page.tsx` `faqData` — 2026-09-09
- Backend API surface is auth + push + probes only, no domain feature — `api/swagger.yml`, `internal/api/handlers/handlers.go`, `internal/models/` — 2026-09-09
- `make sql-check-migrations-unnecessary-null` fails on the string `IS NULL` (seven inverted forms, `NOT NULL` among them) — `Makefile:235-238` — 2026-09-09
- `scripts/sql/default_zero_values.sql` checks only boolean, char/text, integer and numeric `data_type`s, never `USER-DEFINED` — `scripts/sql/default_zero_values.sql:45-52` — 2026-09-09
- `CompleteRegister` sets `is_active`, deletes the token and issues a session without accepting a password — `internal/auth/service.go:588-643` — 2026-09-09
- Transaction executor misuse (`s.db` inside a `WithTransaction` closure) — `internal/auth/service.go:596` was a genuine defect (unlocked read, both concurrent confirmations succeed) and is fixed. `internal/data/local/service_push.go:39` looked identical but was **deliberate**: the error message reads "The new token was saved." and `TestPutUpdatePushTokenWithOldTokenNotfound` asserts the new token survives the 404. Restructured into two explicit transactions preserving that behaviour — 2026-09-10 (corrected; initially misreported as a second bug)
- `DeleteUserAccount` is a bare `DELETE FROM users` relying on FK cascades — `internal/auth/service.go:665` — 2026-09-09
- `CheckUserScopes` uses OR semantics; empty required list allows — `internal/api/middleware/auth.go:236-252` — 2026-09-09
- S-018 and S-019 are marked **fixed**, not deferred; open findings are S-020, S-022, S-023 — `docs/security/2026-09-08-security-audit.md:67,69,71,87,89` — 2026-09-09
- Ship gate protocol: three hard gates, `check-gate.sh` must exit 0, non-empty `## Sources` required — `/home/dokimazo-tech/dev247/ship/core/_conventions.md:63-93` — 2026-09-09
- Paystack signs webhooks with HMAC-SHA512 (hex) over the **raw** body, keyed by the secret key, in `x-paystack-signature` — https://hookdeck.com/webhooks/platforms/guide-to-paystack-webhooks-features-and-best-practices — 2026-09-10
- Paystack webhook signature contains **no timestamp**, so there is no replay window and no built-in event ID; dedupe on event + `data.id`/`reference` — same source — 2026-09-10
- Paystack webhook timeout 30s; retries every 3 min for 4 tries then hourly up to 72 hours (live), hourly for 10 hours (test) — same source — 2026-09-10
- Paystack webhook source IPs 52.31.139.75, 52.49.173.169, 52.214.14.220, same for test and live — same source — 2026-09-10
- Paystack subscriptions require a Plan; cancellation needs both `subscription_code` and `email_token`, which is returned once at creation; changing an amount means a new plan and a new subscription — https://github.com/PaystackHQ/documentation/blob/master/recurring-payments/subscriptions.md — 2026-09-10
