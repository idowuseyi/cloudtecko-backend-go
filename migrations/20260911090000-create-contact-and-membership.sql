-- Contact messages, FAQs, and the membership waitlist.
--
-- Both PII-bearing tables carry purge_after as NOT NULL. Retention is therefore
-- a schema constraint rather than a policy someone remembers: nothing can insert
-- a name, an email address or free text without also stating when it expires.
-- The rows are HARD-deleted by `app db purge-expired`, not soft-deleted --
-- soft-deleted personal data is still personal data under the NDPA 2023.
--
-- membership_applications holds only the HASH of an invitation token, matching
-- what S-018 and S-024 established for every other credential-bearing token in
-- this schema: the value handed to a human is never the value stored.
--
-- No user row is created at approval time. The applicant becomes a user only by
-- accepting the invitation WITH a password. Creating the user at approval and
-- mailing a confirmation link would make that link a bearer credential -- whoever
-- opens the email is signed in -- against an account that has no password set,
-- and which could therefore never be logged into again once the session expired.
-- +migrate Up
CREATE TYPE contact_subject AS ENUM (
    'general',
    'support',
    'partnership'
);

CREATE TYPE contact_message_status AS ENUM (
    'new',
    'read',
    'replied',
    'spam',
    'archived'
);

CREATE TYPE application_status AS ENUM (
    'pending',
    'approved',
    'rejected',
    'withdrawn',
    'accepted',
    'expired'
);

CREATE TABLE contact_messages (
    id uuid NOT NULL DEFAULT uuid_generate_v4 (),
    name text NOT NULL,
    email text NOT NULL,
    subject contact_subject NOT NULL DEFAULT 'general',
    message text NOT NULL,
    status contact_message_status NOT NULL DEFAULT 'new',
    -- Truncated to a /24 (v4) or /48 (v6) prefix at write time. A full address is
    -- personal data and buys nothing for abuse triage that the prefix does not.
    source_ip_prefix text,
    user_agent text,
    handled_by_user_id uuid,
    handled_at timestamptz,
    purge_after timestamptz NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CONSTRAINT contact_messages_pkey PRIMARY KEY (id)
);

CREATE INDEX idx_contact_messages_fk_handled_by_user_id ON contact_messages USING btree (handled_by_user_id);

CREATE INDEX idx_contact_messages_status_created_at ON contact_messages USING btree (status, created_at DESC);

-- Backs the per-email submission throttle in the service layer.
CREATE INDEX idx_contact_messages_email_created_at ON contact_messages USING btree (lower(email), created_at DESC);

CREATE INDEX idx_contact_messages_purge_after ON contact_messages USING btree (purge_after);

ALTER TABLE contact_messages
    ADD CONSTRAINT contact_messages_handled_by_user_id_fkey FOREIGN KEY (handled_by_user_id) REFERENCES users (id) ON UPDATE CASCADE ON DELETE SET NULL;

CREATE TABLE faqs (
    id uuid NOT NULL DEFAULT uuid_generate_v4 (),
    question text NOT NULL,
    answer text NOT NULL,
    category text NOT NULL DEFAULT '',
    sort_order integer NOT NULL DEFAULT 0,
    published_at timestamptz,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CONSTRAINT faqs_pkey PRIMARY KEY (id)
);

CREATE INDEX idx_faqs_category_sort_order ON faqs USING btree (category, sort_order);

CREATE TABLE membership_applications (
    id uuid NOT NULL DEFAULT uuid_generate_v4 (),
    email text NOT NULL,
    full_name text NOT NULL,
    motivation text NOT NULL DEFAULT '',
    organisation text,
    status application_status NOT NULL DEFAULT 'pending',
    invitation_token_hash bytea,
    invitation_valid_until timestamptz,
    reviewed_by_user_id uuid,
    reviewed_at timestamptz,
    review_note text,
    -- Set at ACCEPTANCE, never at approval: no user exists until the applicant
    -- chooses a password, so a rejected or unaccepted application leaves no
    -- orphan account behind.
    accepted_user_id uuid,
    purge_after timestamptz NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CONSTRAINT membership_applications_pkey PRIMARY KEY (id),
    CONSTRAINT membership_applications_invitation_token_hash_key UNIQUE (invitation_token_hash)
);

-- One LIVE application per email. Rejected, withdrawn, expired and accepted rows
-- may repeat, so someone turned down once can apply again later. Filtering on the
-- status enum rather than on a nullable timestamp also keeps the predicate free
-- of an emptiness test, which the migration lint checks for textually -- it greps
-- the file rather than parsing it, so even a comment mentioning the keyword trips
-- it.
CREATE UNIQUE INDEX idx_membership_applications_one_live_per_email ON membership_applications USING btree (lower(email))
WHERE (status IN ('pending', 'approved'));

CREATE INDEX idx_membership_applications_fk_reviewed_by_user_id ON membership_applications USING btree (reviewed_by_user_id);

CREATE INDEX idx_membership_applications_fk_accepted_user_id ON membership_applications USING btree (accepted_user_id);

CREATE INDEX idx_membership_applications_status_created_at ON membership_applications USING btree (status, created_at DESC);

CREATE INDEX idx_membership_applications_purge_after ON membership_applications USING btree (purge_after);

ALTER TABLE membership_applications
    ADD CONSTRAINT membership_applications_reviewed_by_user_id_fkey FOREIGN KEY (reviewed_by_user_id) REFERENCES users (id) ON UPDATE CASCADE ON DELETE SET NULL;

ALTER TABLE membership_applications
    ADD CONSTRAINT membership_applications_accepted_user_id_fkey FOREIGN KEY (accepted_user_id) REFERENCES users (id) ON UPDATE CASCADE ON DELETE SET NULL;

-- +migrate Down
DROP TABLE IF EXISTS membership_applications;

DROP TABLE IF EXISTS faqs;

DROP TABLE IF EXISTS contact_messages;

DROP TYPE IF EXISTS application_status;

DROP TYPE IF EXISTS contact_message_status;

DROP TYPE IF EXISTS contact_subject;

