-- The commerce spine: orders, payments, refunds, webhook events and donations.
--
-- Nothing user-facing uses these yet. They exist first, and alone, because every
-- money flow that follows -- merch, tickets, one-off and recurring donations --
-- shares exactly this machinery, and getting it wrong once is cheaper to fix
-- than getting it wrong four times.
--
-- Money is bigint kobo. No floats anywhere: 0.1 + 0.2 is not 0.3, and a rounding
-- error in a total is a rounding error in someone's bank account.
--
-- A payment belongs to EXACTLY ONE subject, enforced by a CHECK over two
-- nullable FKs rather than a (payable_type, payable_id) pair. The pair form
-- gives up referential integrity and sqlboiler relations for a generality this
-- schema does not need; forcing donations into orders instead would break
-- outright for recurring giving, where one subscription yields many charges over
-- years with no order per charge.
--
-- orders is created here despite nothing writing to it until ticketing, because
-- payments references it. order_items is NOT created yet: it points at product
-- variants and ticket tiers, neither of which exists.
-- +migrate Up
CREATE TYPE currency_code AS ENUM (
    'NGN'
);

CREATE TYPE order_kind AS ENUM (
    'merch',
    'ticket'
);

CREATE TYPE order_status AS ENUM (
    'pending',
    'payment_failed',
    'expired',
    'paid',
    'packed',
    'shipped',
    'delivered',
    'cancelled'
);

CREATE TYPE payment_provider AS ENUM (
    'paystack',
    'mock'
);

CREATE TYPE payment_status AS ENUM (
    'pending',
    'processing',
    'succeeded',
    'failed',
    'abandoned',
    'expired',
    'amount_mismatch',
    'partially_refunded',
    'refunded'
);

CREATE TYPE donation_kind AS ENUM (
    'one_off',
    'recurring'
);

CREATE TYPE donation_status AS ENUM (
    'pending',
    'active',
    'completed',
    'past_due',
    'cancelled',
    'failed'
);

CREATE TABLE orders (
    id uuid NOT NULL DEFAULT uuid_generate_v4 (),
    -- Printed on receipts and used as the provider reference. Unguessable on
    -- purpose: a sequential number would let anyone enumerate other people's
    -- orders through any endpoint that looks one up.
    reference text NOT NULL,
    kind order_kind NOT NULL DEFAULT 'merch',
    status order_status NOT NULL DEFAULT 'pending',
    -- SET NULL, never CASCADE. DeleteUserAccount is a bare DeleteAll on users
    -- that relies on cascades, so a CASCADE here would let an erasure request
    -- destroy financial records that tax law requires retaining.
    user_id uuid,
    email text NOT NULL,
    full_name text NOT NULL,
    phone text,
    -- Snapshotted at checkout: editing a profile in 2027 must not rewrite a 2026
    -- receipt. Absent for ticket orders, required for merch -- a condition on
    -- kind, so it is enforced in the service rather than by a CHECK.
    shipping_line1 text,
    shipping_line2 text,
    shipping_city text,
    shipping_state text,
    shipping_country text,
    shipping_postcode text,
    subtotal_kobo bigint NOT NULL DEFAULT 0,
    shipping_kobo bigint NOT NULL DEFAULT 0,
    tax_kobo bigint NOT NULL DEFAULT 0,
    discount_kobo bigint NOT NULL DEFAULT 0,
    total_kobo bigint NOT NULL DEFAULT 0,
    currency currency_code NOT NULL DEFAULT 'NGN',
    -- The stock/seat reservation hold. Past this, the hold is released.
    reservation_expires_at timestamptz,
    paid_at timestamptz,
    shipped_at timestamptz,
    delivered_at timestamptz,
    cancelled_at timestamptz,
    tracking_reference text,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CONSTRAINT orders_pkey PRIMARY KEY (id),
    CONSTRAINT orders_reference_key UNIQUE (reference),
    CONSTRAINT orders_amounts_check CHECK (subtotal_kobo >= 0 AND shipping_kobo >= 0 AND tax_kobo >= 0 AND discount_kobo >= 0 AND total_kobo >= 0),
    -- Makes an arithmetic mistake a failed INSERT rather than an undercharge.
    CONSTRAINT orders_total_sum_check CHECK (total_kobo = subtotal_kobo + shipping_kobo + tax_kobo - discount_kobo)
);

CREATE INDEX idx_orders_fk_user_id ON orders USING btree (user_id);

CREATE INDEX idx_orders_status_created_at ON orders USING btree (status, created_at DESC);

CREATE INDEX idx_orders_email ON orders USING btree (lower(email));

CREATE INDEX idx_orders_reservation_expires_at ON orders USING btree (reservation_expires_at);

ALTER TABLE orders
    ADD CONSTRAINT orders_user_id_fkey FOREIGN KEY (user_id) REFERENCES users (id) ON UPDATE CASCADE ON DELETE SET NULL;

CREATE TABLE donation_tiers (
    id uuid NOT NULL DEFAULT uuid_generate_v4 (),
    name text NOT NULL,
    description text NOT NULL DEFAULT '',
    amount_kobo bigint NOT NULL,
    currency currency_code NOT NULL DEFAULT 'NGN',
    -- 'monthly' or 'annually'. Paystack fixes an amount to a PLAN, so a price
    -- change means a new tier and a new plan, never an edit to this one.
    interval text NOT NULL DEFAULT '',
    provider_plan_code text,
    sort_order integer NOT NULL DEFAULT 0,
    archived_at timestamptz,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CONSTRAINT donation_tiers_pkey PRIMARY KEY (id),
    CONSTRAINT donation_tiers_amount_check CHECK (amount_kobo > 0)
);

CREATE TABLE donations (
    id uuid NOT NULL DEFAULT uuid_generate_v4 (),
    reference text NOT NULL,
    kind donation_kind NOT NULL DEFAULT 'one_off',
    status donation_status NOT NULL DEFAULT 'pending',
    donation_tier_id uuid,
    user_id uuid,
    email text NOT NULL,
    display_name text,
    is_anonymous boolean NOT NULL DEFAULT FALSE,
    amount_kobo bigint NOT NULL,
    currency currency_code NOT NULL DEFAULT 'NGN',
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CONSTRAINT donations_pkey PRIMARY KEY (id),
    CONSTRAINT donations_reference_key UNIQUE (reference),
    CONSTRAINT donations_amount_check CHECK (amount_kobo > 0),
    -- A recurring donation must name the tier whose plan fixes its amount.
    CONSTRAINT donations_recurring_requires_tier_check CHECK (kind = 'one_off' OR donation_tier_id IS NOT NULL)
);

CREATE INDEX idx_donations_fk_donation_tier_id ON donations USING btree (donation_tier_id);

CREATE INDEX idx_donations_fk_user_id ON donations USING btree (user_id);

CREATE INDEX idx_donations_status_created_at ON donations USING btree (status, created_at DESC);

CREATE INDEX idx_donations_email ON donations USING btree (lower(email));

ALTER TABLE donations
    ADD CONSTRAINT donations_donation_tier_id_fkey FOREIGN KEY (donation_tier_id) REFERENCES donation_tiers (id) ON UPDATE CASCADE ON DELETE RESTRICT;

ALTER TABLE donations
    ADD CONSTRAINT donations_user_id_fkey FOREIGN KEY (user_id) REFERENCES users (id) ON UPDATE CASCADE ON DELETE SET NULL;

CREATE TABLE donation_subscriptions (
    id uuid NOT NULL DEFAULT uuid_generate_v4 (),
    donation_id uuid NOT NULL,
    provider payment_provider NOT NULL DEFAULT 'paystack',
    provider_customer_code text,
    provider_subscription_code text,
    -- Paystack requires BOTH the subscription code and this token to cancel a
    -- subscription, and returns the token exactly once at creation. Without it
    -- stored, a donor can never stop giving through our own UI. It cannot be
    -- hashed -- it must be replayed to the provider -- so it is the one
    -- credential here that is stored reversibly.
    provider_email_token text,
    next_charge_at timestamptz,
    cancelled_at timestamptz,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CONSTRAINT donation_subscriptions_pkey PRIMARY KEY (id),
    CONSTRAINT donation_subscriptions_donation_id_key UNIQUE (donation_id)
);

CREATE INDEX idx_donation_subscriptions_provider_subscription_code ON donation_subscriptions USING btree (provider_subscription_code);

ALTER TABLE donation_subscriptions
    ADD CONSTRAINT donation_subscriptions_donation_id_fkey FOREIGN KEY (donation_id) REFERENCES donations (id) ON UPDATE CASCADE ON DELETE CASCADE;

CREATE TABLE payments (
    id uuid NOT NULL DEFAULT uuid_generate_v4 (),
    provider payment_provider NOT NULL DEFAULT 'paystack',
    status payment_status NOT NULL DEFAULT 'pending',
    order_id uuid,
    donation_id uuid,
    -- OUR reference, sent to the provider at initialize.
    reference text NOT NULL,
    -- What the provider calls it, learned from verify or the webhook.
    provider_reference text,
    provider_transaction_id text,
    provider_authorization_code text,
    amount_kobo bigint NOT NULL,
    currency currency_code NOT NULL DEFAULT 'NGN',
    amount_settled_kobo bigint,
    amount_refunded_kobo bigint NOT NULL DEFAULT 0,
    channel text,
    authorization_url text,
    -- Intents expire; this is what releases a stock or seat reservation.
    expires_at timestamptz NOT NULL,
    -- Records that THIS payment's reservation has been given back. Exactly one
    -- actor may release a hold, and it claims that right by updating this
    -- column conditionally. Without it, a webhook arriving after lazy expiry
    -- already released the hold would decrement a reservation that no longer
    -- exists, drive the counter negative, violate its CHECK, abort the
    -- transaction and return non-2xx -- money captured, nothing recorded, and
    -- the provider retrying for 72 hours.
    hold_released_at timestamptz,
    succeeded_at timestamptz,
    failed_at timestamptz,
    failure_reason text,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CONSTRAINT payments_pkey PRIMARY KEY (id),
    CONSTRAINT payments_reference_key UNIQUE (reference),
    CONSTRAINT payments_provider_transaction_id_key UNIQUE (provider, provider_transaction_id),
    CONSTRAINT payments_amount_check CHECK (amount_kobo > 0 AND amount_refunded_kobo >= 0 AND amount_refunded_kobo <= amount_kobo),
    CONSTRAINT payments_exactly_one_subject_check CHECK ((order_id IS NOT NULL)::integer + (donation_id IS NOT NULL)::integer = 1)
);

CREATE INDEX idx_payments_fk_order_id ON payments USING btree (order_id);

CREATE INDEX idx_payments_fk_donation_id ON payments USING btree (donation_id);

CREATE INDEX idx_payments_status_expires_at ON payments USING btree (status, expires_at);

ALTER TABLE payments
    ADD CONSTRAINT payments_order_id_fkey FOREIGN KEY (order_id) REFERENCES orders (id) ON UPDATE CASCADE ON DELETE RESTRICT;

ALTER TABLE payments
    ADD CONSTRAINT payments_donation_id_fkey FOREIGN KEY (donation_id) REFERENCES donations (id) ON UPDATE CASCADE ON DELETE RESTRICT;

CREATE TABLE payment_refunds (
    id uuid NOT NULL DEFAULT uuid_generate_v4 (),
    payment_id uuid NOT NULL,
    amount_kobo bigint NOT NULL,
    reason text NOT NULL DEFAULT '',
    status text NOT NULL DEFAULT '',
    provider_refund_id text,
    requested_by_user_id uuid,
    restock boolean NOT NULL DEFAULT FALSE,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CONSTRAINT payment_refunds_pkey PRIMARY KEY (id),
    CONSTRAINT payment_refunds_amount_check CHECK (amount_kobo > 0)
);

CREATE INDEX idx_payment_refunds_fk_payment_id ON payment_refunds USING btree (payment_id);

CREATE INDEX idx_payment_refunds_fk_requested_by_user_id ON payment_refunds USING btree (requested_by_user_id);

ALTER TABLE payment_refunds
    ADD CONSTRAINT payment_refunds_payment_id_fkey FOREIGN KEY (payment_id) REFERENCES payments (id) ON UPDATE CASCADE ON DELETE RESTRICT;

ALTER TABLE payment_refunds
    ADD CONSTRAINT payment_refunds_requested_by_user_id_fkey FOREIGN KEY (requested_by_user_id) REFERENCES users (id) ON UPDATE CASCADE ON DELETE SET NULL;

CREATE TABLE payment_webhook_events (
    id uuid NOT NULL DEFAULT uuid_generate_v4 (),
    provider payment_provider NOT NULL DEFAULT 'paystack',
    event_type text NOT NULL,
    -- '<event_type>:<provider event/data id>'. Deliberately NOT a hash of the
    -- body: a digest changes with any re-serialisation or recomputed field, so
    -- it can only ever MISS duplicates, never catch more.
    idempotency_key text NOT NULL,
    provider_reference text,
    payload jsonb NOT NULL,
    signature text NOT NULL DEFAULT '',
    received_at timestamptz NOT NULL,
    processed_at timestamptz,
    process_error text,
    process_attempts integer NOT NULL DEFAULT 0,
    CONSTRAINT payment_webhook_events_pkey PRIMARY KEY (id),
    CONSTRAINT payment_webhook_events_idempotency_key UNIQUE (provider, idempotency_key)
);

CREATE INDEX idx_payment_webhook_events_provider_reference ON payment_webhook_events USING btree (provider_reference);

CREATE INDEX idx_payment_webhook_events_received_at ON payment_webhook_events USING btree (received_at DESC);

-- Unprocessed events are the operational alarm for "money taken, nothing
-- recorded". Keep them cheap to find.
CREATE INDEX idx_payment_webhook_events_unprocessed ON payment_webhook_events USING btree (received_at)
WHERE (processed_at IS NULL);

-- +migrate Down
DROP TABLE IF EXISTS payment_webhook_events;

DROP TABLE IF EXISTS payment_refunds;

DROP TABLE IF EXISTS payments;

DROP TABLE IF EXISTS donation_subscriptions;

DROP TABLE IF EXISTS donations;

DROP TABLE IF EXISTS donation_tiers;

DROP TABLE IF EXISTS orders;

DROP TYPE IF EXISTS donation_status;

DROP TYPE IF EXISTS donation_kind;

DROP TYPE IF EXISTS payment_status;

DROP TYPE IF EXISTS payment_provider;

DROP TYPE IF EXISTS order_status;

DROP TYPE IF EXISTS order_kind;

DROP TYPE IF EXISTS currency_code;

