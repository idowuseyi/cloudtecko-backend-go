-- Learning resources, with full-text search.
--
-- search_tsv is a GENERATED column rather than a trigger-maintained one: a
-- trigger is a second place the definition lives and drifts from silently.
--
-- to_tsvector MUST be called in its two-argument form here. The one-argument
-- form reads default_text_search_config and is therefore only STABLE, and
-- PostgreSQL rejects a non-IMMUTABLE expression in a generated column. Using the
-- one-arg form is the most common way this migration fails to apply.
--
-- Weighting: title (A) outranks summary (B), author (C) and body (D), so a query
-- matching a title sorts above one that merely appears in a paragraph.
--
-- NOTE: resources.search_tsv is blacklisted in sqlboiler.toml. Without that,
-- boil.Infer() includes the column in every INSERT and PostgreSQL answers
-- 'column "search_tsv" can only be updated to DEFAULT'.
-- +migrate Up
CREATE TYPE resource_kind AS ENUM (
    'article',
    'guide',
    'video',
    'download',
    'link'
);

CREATE TABLE resources (
    id uuid NOT NULL DEFAULT uuid_generate_v4 (),
    slug text NOT NULL,
    title text NOT NULL,
    summary text NOT NULL,
    body text NOT NULL DEFAULT '',
    kind resource_kind NOT NULL DEFAULT 'article',
    category_id uuid NOT NULL,
    hero_media_id uuid,
    author text NOT NULL DEFAULT '',
    content_url text,
    reading_time_minutes integer NOT NULL DEFAULT 0,
    published_at timestamptz,
    archived_at timestamptz,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    search_tsv tsvector GENERATED ALWAYS AS (setweight(to_tsvector('english', COALESCE(title, '')), 'A') || setweight(to_tsvector('english', COALESCE(summary, '')), 'B') || setweight(to_tsvector('english', COALESCE(author, '')), 'C') || setweight(to_tsvector('english', COALESCE(body, '')), 'D')) STORED,
    CONSTRAINT resources_pkey PRIMARY KEY (id),
    CONSTRAINT resources_slug_key UNIQUE (slug),
    CONSTRAINT resources_reading_time_check CHECK (reading_time_minutes >= 0)
);

CREATE INDEX idx_resources_fk_category_id ON resources USING btree (category_id);

CREATE INDEX idx_resources_fk_hero_media_id ON resources USING btree (hero_media_id);

CREATE INDEX idx_resources_published_at ON resources USING btree (published_at DESC);

CREATE INDEX idx_resources_search_tsv ON resources USING GIN (search_tsv);

ALTER TABLE resources
    ADD CONSTRAINT resources_category_id_fkey FOREIGN KEY (category_id) REFERENCES categories (id) ON UPDATE CASCADE ON DELETE RESTRICT;

ALTER TABLE resources
    ADD CONSTRAINT resources_hero_media_id_fkey FOREIGN KEY (hero_media_id) REFERENCES media_assets (id) ON UPDATE CASCADE ON DELETE SET NULL;

-- +migrate Down
DROP TABLE IF EXISTS resources;

DROP TYPE IF EXISTS resource_kind;

