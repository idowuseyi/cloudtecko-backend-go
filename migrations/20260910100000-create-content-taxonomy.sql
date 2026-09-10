-- Shared taxonomy and media for the content surface (events and resources).
--
-- categories is ONE table serving both content types, discriminated by scope.
-- That is deliberately the opposite call from events/resources themselves, which
-- are kept as two tables: the distinction is that a category has IDENTICAL
-- columns in both scopes, whereas an event and a resource do not -- an event
-- needs start_at/venue/capacity that would all have to be nullable if the two
-- shared a table, which would put the "an event always has a start time" rule
-- beyond what the database can enforce.
--
-- media_assets tracks uploads through a quarantine -> verify -> promote cycle.
-- A presigned PUT cannot constrain the bytes a client actually sends, so status
-- stays 'pending' until the server has issued a HEAD for the true size and
-- sniffed the leading bytes for the real content type.
-- +migrate Up
CREATE TYPE category_scope AS ENUM (
    'event',
    'resource'
);

CREATE TYPE media_status AS ENUM (
    'pending',
    'ready',
    'rejected'
);

CREATE TABLE categories (
    id uuid NOT NULL DEFAULT uuid_generate_v4 (),
    scope category_scope NOT NULL,
    slug text NOT NULL,
    name text NOT NULL,
    sort_order integer NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CONSTRAINT categories_pkey PRIMARY KEY (id),
    CONSTRAINT categories_scope_slug_key UNIQUE (scope, slug)
);

CREATE INDEX idx_categories_scope_sort_order ON categories USING btree (scope, sort_order);

CREATE TABLE media_assets (
    id uuid NOT NULL DEFAULT uuid_generate_v4 (),
    object_key text NOT NULL,
    bucket text NOT NULL,
    content_type text NOT NULL,
    byte_size bigint NOT NULL DEFAULT 0,
    checksum_sha256 text,
    status media_status NOT NULL DEFAULT 'pending',
    -- SET NULL, never CASCADE: DeleteUserAccount is a bare DeleteAll on users
    -- (internal/auth/service.go:544) that relies entirely on cascades, so a
    -- CASCADE here would let an erasure request delete published site imagery.
    uploaded_by_user_id uuid,
    public_url text,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CONSTRAINT media_assets_pkey PRIMARY KEY (id),
    CONSTRAINT media_assets_object_key_key UNIQUE (object_key),
    CONSTRAINT media_assets_byte_size_check CHECK (byte_size >= 0)
);

CREATE INDEX idx_media_assets_fk_uploaded_by_user_id ON media_assets USING btree (uploaded_by_user_id);

-- Backs the orphan sweep for uploads that were presigned but never completed.
CREATE INDEX idx_media_assets_status_created_at ON media_assets USING btree (status, created_at);

ALTER TABLE media_assets
    ADD CONSTRAINT media_assets_uploaded_by_user_id_fkey FOREIGN KEY (uploaded_by_user_id) REFERENCES users (id) ON UPDATE CASCADE ON DELETE SET NULL;

-- +migrate Down
DROP TABLE IF EXISTS media_assets;

DROP TABLE IF EXISTS categories;

DROP TYPE IF EXISTS media_status;

DROP TYPE IF EXISTS category_scope;

