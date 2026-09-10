-- Store only a hash of password-reset and confirmation tokens, for the same
-- reason as session tokens (see 20260908090000): read access to the database
-- must not be equivalent to holding a live credential.
--
-- These are arguably worse than a session token if leaked -- a password-reset
-- token is a direct path to account takeover -- and the fact that they are
-- short-lived and single-use only narrows the window, it does not close it.
--
-- BEHAVIOUR CHANGE: the password-reset "reuse window" is removed by this change.
-- It re-sent the SAME token when a user asked twice within the window, which
-- requires reading the plaintext back and is impossible once only a hash is
-- stored. Requesting a reset now invalidates any earlier one, which is both the
-- stronger option (exactly one live reset token per user) and what users expect
-- from password reset elsewhere.
-- +migrate Up
-- password_reset_tokens ----------------------------------------------------
ALTER TABLE password_reset_tokens
    ADD COLUMN token_hash bytea;

UPDATE
    password_reset_tokens
SET
    token_hash = sha256 (convert_to(token::text, 'UTF8'));

ALTER TABLE password_reset_tokens
    ALTER COLUMN token_hash SET NOT NULL;

ALTER TABLE password_reset_tokens
    DROP CONSTRAINT password_reset_tokens_pkey;

ALTER TABLE password_reset_tokens
    DROP COLUMN token;

ALTER TABLE password_reset_tokens
    ADD CONSTRAINT password_reset_tokens_pkey PRIMARY KEY (token_hash);

-- confirmation_tokens ------------------------------------------------------
ALTER TABLE confirmation_tokens
    ADD COLUMN token_hash bytea;

UPDATE
    confirmation_tokens
SET
    token_hash = sha256 (convert_to(token::text, 'UTF8'));

ALTER TABLE confirmation_tokens
    ALTER COLUMN token_hash SET NOT NULL;

ALTER TABLE confirmation_tokens
    DROP CONSTRAINT confirmation_tokens_pkey;

ALTER TABLE confirmation_tokens
    DROP COLUMN token;

ALTER TABLE confirmation_tokens
    ADD CONSTRAINT confirmation_tokens_pkey PRIMARY KEY (token_hash);

-- +migrate Down
ALTER TABLE password_reset_tokens
    ADD COLUMN token uuid NOT NULL DEFAULT uuid_generate_v4 ();

ALTER TABLE password_reset_tokens
    DROP CONSTRAINT password_reset_tokens_pkey;

ALTER TABLE password_reset_tokens
    DROP COLUMN token_hash;

ALTER TABLE password_reset_tokens
    ADD CONSTRAINT password_reset_tokens_pkey PRIMARY KEY (token);

ALTER TABLE confirmation_tokens
    ADD COLUMN token uuid NOT NULL DEFAULT uuid_generate_v4 ();

ALTER TABLE confirmation_tokens
    DROP CONSTRAINT confirmation_tokens_pkey;

ALTER TABLE confirmation_tokens
    DROP COLUMN token_hash;

ALTER TABLE confirmation_tokens
    ADD CONSTRAINT confirmation_tokens_pkey PRIMARY KEY (token);

