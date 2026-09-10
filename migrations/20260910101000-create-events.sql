-- Events.
--
-- Time is stored as an absolute instant (start_at/end_at) PLUS the IANA zone the
-- organiser meant. timestamptz alone is unambiguous for sorting and reminders but
-- loses the wall-clock intent, which matters the moment an event moves country or
-- a zone changes its offset. The frontend renders "9AM - 5PM WAT" from these three
-- columns; that string is never stored, because a stored rendering cannot be
-- queried for "upcoming events" and cannot drive a reminder.
--
-- published_at and archived_at carry visibility; there is no deleted_at. A soft
-- delete means every query in the system needs a filter, and the one query that
-- forgets it leaks unpublished content -- sqlboiler will not remind us, since the
-- repo does not use its soft-delete support.
-- +migrate Up
CREATE TABLE events (
    id uuid NOT NULL DEFAULT uuid_generate_v4 (),
    slug text NOT NULL,
    title text NOT NULL,
    summary text NOT NULL,
    body text NOT NULL DEFAULT '',
    category_id uuid NOT NULL,
    hero_media_id uuid,
    start_at timestamptz NOT NULL,
    end_at timestamptz NOT NULL,
    -- IANA name, e.g. 'Africa/Lagos'.
    timezone text NOT NULL,
    is_online boolean NOT NULL DEFAULT FALSE,
    venue_name text,
    venue_address text,
    meeting_url text,
    is_featured boolean NOT NULL DEFAULT FALSE,
    published_at timestamptz,
    archived_at timestamptz,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CONSTRAINT events_pkey PRIMARY KEY (id),
    CONSTRAINT events_slug_key UNIQUE (slug),
    CONSTRAINT events_time_order_check CHECK (end_at >= start_at)
);

CREATE INDEX idx_events_fk_category_id ON events USING btree (category_id);

CREATE INDEX idx_events_fk_hero_media_id ON events USING btree (hero_media_id);

CREATE INDEX idx_events_published_start_at ON events USING btree (published_at, start_at DESC);

-- Makes "the featured event" a database invariant rather than an admin
-- convention: promoting a new one must demote the old one in the same
-- transaction, and the database refuses two.
CREATE UNIQUE INDEX idx_events_single_featured ON events USING btree (is_featured)
WHERE (is_featured AND published_at IS NOT NULL);

-- RESTRICT, not CASCADE: deleting a category that still has events should fail
-- loudly rather than silently destroy published content.
ALTER TABLE events
    ADD CONSTRAINT events_category_id_fkey FOREIGN KEY (category_id) REFERENCES categories (id) ON UPDATE CASCADE ON DELETE RESTRICT;

ALTER TABLE events
    ADD CONSTRAINT events_hero_media_id_fkey FOREIGN KEY (hero_media_id) REFERENCES media_assets (id) ON UPDATE CASCADE ON DELETE SET NULL;

-- +migrate Down
DROP TABLE IF EXISTS events;

