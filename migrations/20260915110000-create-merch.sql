-- Merch: products, variants, shipping zones, and the FK that completes
-- order_items.
--
-- Variants exist because community merch is sold in sizes. Modelling stock on
-- the product instead would mean "12 hoodies left" with no way to say which are
-- mediums -- and the first person to order a size that is gone finds out after
-- paying.
--
-- Shipping is a ZONE TABLE where a flat nationwide rate is a single row matching
-- every state. That makes flat a configuration of the general model rather than
-- a limitation of it, so moving to per-state pricing later is data entry, not a
-- migration. Nigeria has 36 states plus the FCT, and intra-Lagos versus
-- interstate is a real difference in cost.
-- +migrate Up
CREATE TABLE products (
    id uuid NOT NULL DEFAULT uuid_generate_v4 (),
    slug text NOT NULL,
    title text NOT NULL,
    description text NOT NULL DEFAULT '',
    hero_media_id uuid,
    published_at timestamptz,
    archived_at timestamptz,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CONSTRAINT products_pkey PRIMARY KEY (id),
    CONSTRAINT products_slug_key UNIQUE (slug)
);

CREATE INDEX idx_products_fk_hero_media_id ON products USING btree (hero_media_id);

CREATE INDEX idx_products_published_at ON products USING btree (published_at DESC);

ALTER TABLE products
    ADD CONSTRAINT products_hero_media_id_fkey FOREIGN KEY (hero_media_id) REFERENCES media_assets (id) ON UPDATE CASCADE ON DELETE SET NULL;

CREATE TABLE product_variants (
    id uuid NOT NULL DEFAULT uuid_generate_v4 (),
    product_id uuid NOT NULL,
    sku text NOT NULL,
    size text,
    colour text,
    price_kobo bigint NOT NULL DEFAULT 0,
    currency currency_code NOT NULL DEFAULT 'NGN',
    stock_total integer NOT NULL DEFAULT 0,
    stock_reserved integer NOT NULL DEFAULT 0,
    stock_sold integer NOT NULL DEFAULT 0,
    -- Kept even while shipping is flat, so switching to weight-based pricing
    -- later is a rate-table change rather than a backfill of every variant.
    weight_grams integer NOT NULL DEFAULT 0,
    archived_at timestamptz,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CONSTRAINT product_variants_pkey PRIMARY KEY (id),
    CONSTRAINT product_variants_sku_key UNIQUE (sku),
    CONSTRAINT product_variants_price_check CHECK (price_kobo >= 0),
    -- Same shape as ticket tiers, and for the same reason: stock_sold is allowed
    -- to pass stock_total, because a captured payment whose hold expired must
    -- never be refused. Capacity lives in the reservation predicate.
    CONSTRAINT product_variants_stock_check CHECK (stock_reserved >= 0 AND stock_sold >= 0 AND stock_total >= 0),
    CONSTRAINT product_variants_weight_check CHECK (weight_grams >= 0)
);

CREATE INDEX idx_product_variants_fk_product_id ON product_variants USING btree (product_id);

ALTER TABLE product_variants
    ADD CONSTRAINT product_variants_product_id_fkey FOREIGN KEY (product_id) REFERENCES products (id) ON UPDATE CASCADE ON DELETE RESTRICT;

CREATE TABLE shipping_zones (
    id uuid NOT NULL DEFAULT uuid_generate_v4 (),
    name text NOT NULL,
    -- Empty means "every state not matched by a more specific zone", which is
    -- how one flat nationwide row works.
    states text[] NOT NULL DEFAULT '{}',
    rate_kobo bigint NOT NULL DEFAULT 0,
    -- A subtotal at or above this ships free. Zero disables the threshold
    -- rather than making everything free, which is checked in the service.
    free_above_kobo bigint NOT NULL DEFAULT 0,
    -- Lower sorts first; the first matching zone wins, so a specific zone must
    -- sort ahead of the catch-all.
    sort_order integer NOT NULL DEFAULT 0,
    archived_at timestamptz,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CONSTRAINT shipping_zones_pkey PRIMARY KEY (id),
    CONSTRAINT shipping_zones_rate_check CHECK (rate_kobo >= 0 AND free_above_kobo >= 0)
);

CREATE INDEX idx_shipping_zones_sort_order ON shipping_zones USING btree (sort_order);

-- Completes order_items, whose product_variant_id was created without a foreign
-- key because product_variants did not exist yet.
ALTER TABLE order_items
    ADD CONSTRAINT order_items_product_variant_id_fkey FOREIGN KEY (product_variant_id) REFERENCES product_variants (id) ON UPDATE CASCADE ON DELETE RESTRICT;

-- +migrate Down
ALTER TABLE order_items
    DROP CONSTRAINT IF EXISTS order_items_product_variant_id_fkey;

DROP TABLE IF EXISTS shipping_zones;

DROP TABLE IF EXISTS product_variants;

DROP TABLE IF EXISTS products;

