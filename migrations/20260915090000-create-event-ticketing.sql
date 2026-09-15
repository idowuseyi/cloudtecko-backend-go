-- Event ticketing: tiers, registrations, and the order lines that pay for them.
--
-- Capacity is three counters rather than a computed count of registrations.
-- quantity_reserved is what in-flight checkouts are holding, quantity_sold is
-- what has been paid for, and quantity_total is the cap. Counting rows instead
-- would require locking every registration for an event on every checkout.
--
-- Read the counters CHECK carefully, because one omission in it is deliberate:
--
--     CHECK (quantity_reserved >= 0 AND quantity_sold >= 0 AND quantity_total >= 0)
--
-- There is NO "reserved + sold <= total". Capacity is enforced in the
-- reservation statement's WHERE clause, where breaching it returns zero rows
-- affected and the caller answers 409. Encoding the cap as a CHECK instead would
-- make an over-capacity write an aborted transaction, and there is one case
-- where over-capacity MUST be allowed to succeed: a payment captured by the
-- provider whose hold had already expired. Refusing it would mean money taken
-- and nothing recorded, with the provider retrying for three days. So sold is
-- permitted to exceed total, the registration is stamped oversold_at, and an
-- admin resolves it.
--
-- A price of 0 is a free RSVP tier. Free and paid tiers coexist on one event,
-- which is why price is a per-tier column rather than a flag on the event.

-- +migrate Up
CREATE TYPE registration_status AS ENUM (
    'reserved',
    'confirmed',
    'cancelled',
    'refunded'
);

CREATE TABLE event_ticket_tiers (
    id uuid NOT NULL DEFAULT uuid_generate_v4 (),
    event_id uuid NOT NULL,
    name text NOT NULL,
    description text NOT NULL DEFAULT '',
    price_kobo bigint NOT NULL DEFAULT 0,
    currency currency_code NOT NULL DEFAULT 'NGN',
    quantity_total integer NOT NULL DEFAULT 0,
    quantity_reserved integer NOT NULL DEFAULT 0,
    quantity_sold integer NOT NULL DEFAULT 0,
    -- Bounds a single checkout. Without it one caller can reserve an entire
    -- event in one request.
    max_per_order integer NOT NULL DEFAULT 0,
    sales_start_at timestamptz,
    sales_end_at timestamptz,
    sort_order integer NOT NULL DEFAULT 0,
    archived_at timestamptz,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CONSTRAINT event_ticket_tiers_pkey PRIMARY KEY (id),
    CONSTRAINT event_ticket_tiers_price_check CHECK (price_kobo >= 0),
    CONSTRAINT event_ticket_tiers_counters_check CHECK (quantity_reserved >= 0 AND quantity_sold >= 0 AND quantity_total >= 0),
    CONSTRAINT event_ticket_tiers_max_per_order_check CHECK (max_per_order >= 0),
    CONSTRAINT event_ticket_tiers_sales_window_check CHECK (sales_end_at IS NULL OR sales_start_at IS NULL OR sales_end_at >= sales_start_at)
);

CREATE INDEX idx_event_ticket_tiers_fk_event_id ON event_ticket_tiers USING btree (event_id);

CREATE INDEX idx_event_ticket_tiers_event_sort ON event_ticket_tiers USING btree (event_id, sort_order);

-- CASCADE here, unlike everywhere else: a tier has no meaning apart from its
-- event, and events themselves are archived rather than deleted once published.
ALTER TABLE event_ticket_tiers
    ADD CONSTRAINT event_ticket_tiers_event_id_fkey FOREIGN KEY (event_id) REFERENCES events (id) ON UPDATE CASCADE ON DELETE CASCADE;

CREATE TABLE event_registrations (
    id uuid NOT NULL DEFAULT uuid_generate_v4 (),
    event_id uuid NOT NULL,
    ticket_tier_id uuid NOT NULL,
    -- Unset for a free RSVP: there is no order because there is no money.
    order_id uuid,
    -- SET NULL rather than CASCADE. DeleteUserAccount is a bare DeleteAll on
    -- users relying on cascades, and an erasure request must not destroy the
    -- attendance record of an event that already happened.
    user_id uuid,
    email text NOT NULL,
    full_name text NOT NULL,
    quantity integer NOT NULL DEFAULT 0,
    unit_price_kobo bigint NOT NULL DEFAULT 0,
    status registration_status NOT NULL DEFAULT 'reserved',
    ticket_code text NOT NULL,
    -- Stamped when a seat was granted past capacity because a captured payment
    -- arrived after its hold expired. An admin dashboard filters on this.
    oversold_at timestamptz,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CONSTRAINT event_registrations_pkey PRIMARY KEY (id),
    CONSTRAINT event_registrations_ticket_code_key UNIQUE (ticket_code),
    CONSTRAINT event_registrations_quantity_check CHECK (quantity > 0),
    CONSTRAINT event_registrations_price_check CHECK (unit_price_kobo >= 0)
);

CREATE INDEX idx_event_registrations_fk_event_id ON event_registrations USING btree (event_id);

CREATE INDEX idx_event_registrations_fk_ticket_tier_id ON event_registrations USING btree (ticket_tier_id);

CREATE INDEX idx_event_registrations_fk_order_id ON event_registrations USING btree (order_id);

CREATE INDEX idx_event_registrations_fk_user_id ON event_registrations USING btree (user_id);

CREATE INDEX idx_event_registrations_email ON event_registrations USING btree (lower(email));

-- One LIVE registration per address per event, so a double-clicked RSVP does not
-- become two seats. Cancelled and refunded rows are excluded through the status
-- enum, which also lets someone who cancelled register again later.
CREATE UNIQUE INDEX idx_event_registrations_one_live_per_email ON event_registrations USING btree (event_id, lower(email))
WHERE (status IN ('reserved', 'confirmed'));

ALTER TABLE event_registrations
    ADD CONSTRAINT event_registrations_event_id_fkey FOREIGN KEY (event_id) REFERENCES events (id) ON UPDATE CASCADE ON DELETE RESTRICT;

ALTER TABLE event_registrations
    ADD CONSTRAINT event_registrations_ticket_tier_id_fkey FOREIGN KEY (ticket_tier_id) REFERENCES event_ticket_tiers (id) ON UPDATE CASCADE ON DELETE RESTRICT;

ALTER TABLE event_registrations
    ADD CONSTRAINT event_registrations_order_id_fkey FOREIGN KEY (order_id) REFERENCES orders (id) ON UPDATE CASCADE ON DELETE SET NULL;

ALTER TABLE event_registrations
    ADD CONSTRAINT event_registrations_user_id_fkey FOREIGN KEY (user_id) REFERENCES users (id) ON UPDATE CASCADE ON DELETE SET NULL;

CREATE TABLE order_items (
    id uuid NOT NULL DEFAULT uuid_generate_v4 (),
    order_id uuid NOT NULL,
    -- Exactly one subject per line. product_variant_id has no foreign key yet
    -- because product_variants does not exist until the merch phase; that
    -- constraint is added there, alongside the table it points at.
    ticket_tier_id uuid,
    product_variant_id uuid,
    -- Denormalised at checkout so a rename or reprice in 2027 cannot rewrite a
    -- 2026 receipt.
    title_snapshot text NOT NULL DEFAULT '',
    variant_snapshot text NOT NULL DEFAULT '',
    unit_price_kobo bigint NOT NULL DEFAULT 0,
    quantity integer NOT NULL DEFAULT 0,
    quantity_fulfilled integer NOT NULL DEFAULT 0,
    line_total_kobo bigint NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CONSTRAINT order_items_pkey PRIMARY KEY (id),
    CONSTRAINT order_items_quantity_check CHECK (quantity > 0 AND quantity_fulfilled >= 0 AND quantity_fulfilled <= quantity),
    CONSTRAINT order_items_line_total_check CHECK (line_total_kobo = unit_price_kobo * quantity),
    CONSTRAINT order_items_exactly_one_subject_check CHECK ((ticket_tier_id IS NOT NULL)::integer + (product_variant_id IS NOT NULL)::integer = 1)
);

CREATE INDEX idx_order_items_fk_order_id ON order_items USING btree (order_id);

CREATE INDEX idx_order_items_fk_ticket_tier_id ON order_items USING btree (ticket_tier_id);

CREATE INDEX idx_order_items_fk_product_variant_id ON order_items USING btree (product_variant_id);

ALTER TABLE order_items
    ADD CONSTRAINT order_items_order_id_fkey FOREIGN KEY (order_id) REFERENCES orders (id) ON UPDATE CASCADE ON DELETE CASCADE;

ALTER TABLE order_items
    ADD CONSTRAINT order_items_ticket_tier_id_fkey FOREIGN KEY (ticket_tier_id) REFERENCES event_ticket_tiers (id) ON UPDATE CASCADE ON DELETE RESTRICT;

-- +migrate Down
DROP TABLE IF EXISTS order_items;

DROP TABLE IF EXISTS event_registrations;

DROP TABLE IF EXISTS event_ticket_tiers;

DROP TYPE IF EXISTS registration_status;
