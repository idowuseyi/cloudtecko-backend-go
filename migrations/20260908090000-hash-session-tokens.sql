-- Store only a hash of access and refresh tokens, so that read access to the
-- database is no longer equivalent to holding every live session.
--
-- Previously the token column WAS the value handed to the client, so a leaked
-- backup, a SQL injection or an over-privileged analytics user meant immediate
-- session takeover for every active user (security audit S-018).
--
-- SHA-256 is the right primitive here, not Argon2: these tokens are 122 bits of
-- CSPRNG output, so there is nothing to brute force, and the hash is computed on
-- every authenticated request.
--
-- Refresh tokens additionally gain an expiry, a family id and a consumed marker,
-- so a stolen refresh token is neither valid forever nor silently replayable
-- (S-019).
-- +migrate Up
-- access_tokens ------------------------------------------------------------
ALTER TABLE access_tokens
    ADD COLUMN token_hash bytea;

-- Existing rows can be migrated in place: we still have the plaintext here, and
-- this is the last moment at which that is true.
UPDATE
    access_tokens
SET
    token_hash = sha256 (convert_to(token::text, 'UTF8'));

ALTER TABLE access_tokens
    ALTER COLUMN token_hash SET NOT NULL;

ALTER TABLE access_tokens
    DROP CONSTRAINT access_tokens_pkey;

ALTER TABLE access_tokens
    DROP COLUMN token;

ALTER TABLE access_tokens
    ADD CONSTRAINT access_tokens_pkey PRIMARY KEY (token_hash);

-- refresh_tokens -----------------------------------------------------------
ALTER TABLE refresh_tokens
    ADD COLUMN token_hash bytea;

UPDATE
    refresh_tokens
SET
    token_hash = sha256 (convert_to(token::text, 'UTF8'));

ALTER TABLE refresh_tokens
    ALTER COLUMN token_hash SET NOT NULL;

ALTER TABLE refresh_tokens
    DROP CONSTRAINT refresh_tokens_pkey;

ALTER TABLE refresh_tokens
    DROP COLUMN token;

ALTER TABLE refresh_tokens
    ADD CONSTRAINT refresh_tokens_pkey PRIMARY KEY (token_hash);

-- Refresh tokens had no expiry column at all, so they were valid forever.
-- Existing rows get a validity measured from when they were issued.
ALTER TABLE refresh_tokens
    ADD COLUMN valid_until timestamptz;

UPDATE
    refresh_tokens
SET
    valid_until = created_at + INTERVAL '30 days';

ALTER TABLE refresh_tokens
    ALTER COLUMN valid_until SET NOT NULL;

-- family_id ties a rotation chain together. Rotating issues a new token in the
-- same family; presenting an already-consumed token means the chain leaked, and
-- the whole family is revoked.
ALTER TABLE refresh_tokens
    ADD COLUMN family_id uuid NOT NULL DEFAULT uuid_generate_v4 ();

-- Consumed tokens are retained (rather than deleted) precisely so that replay
-- can be detected. They are cleaned up once valid_until has passed.
ALTER TABLE refresh_tokens
    ADD COLUMN consumed_at timestamptz;

CREATE INDEX idx_refresh_tokens_family_id ON refresh_tokens USING btree (family_id);

CREATE INDEX idx_refresh_tokens_valid_until ON refresh_tokens USING btree (valid_until);

-- +migrate Down
ALTER TABLE access_tokens
    ADD COLUMN token uuid NOT NULL DEFAULT uuid_generate_v4 ();

ALTER TABLE access_tokens
    DROP CONSTRAINT access_tokens_pkey;

ALTER TABLE access_tokens
    DROP COLUMN token_hash;

ALTER TABLE access_tokens
    ADD CONSTRAINT access_tokens_pkey PRIMARY KEY (token);

DROP INDEX IF EXISTS idx_refresh_tokens_valid_until;

DROP INDEX IF EXISTS idx_refresh_tokens_family_id;

ALTER TABLE refresh_tokens
    ADD COLUMN token uuid NOT NULL DEFAULT uuid_generate_v4 ();

ALTER TABLE refresh_tokens
    DROP CONSTRAINT refresh_tokens_pkey;

ALTER TABLE refresh_tokens
    DROP COLUMN token_hash;

ALTER TABLE refresh_tokens
    DROP COLUMN valid_until;

ALTER TABLE refresh_tokens
    DROP COLUMN family_id;

ALTER TABLE refresh_tokens
    DROP COLUMN consumed_at;

ALTER TABLE refresh_tokens
    ADD CONSTRAINT refresh_tokens_pkey PRIMARY KEY (token);

