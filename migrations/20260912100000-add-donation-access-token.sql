-- A donation's access token, for checking its status after checkout.
--
-- Donations are guest-friendly: there is no account to scope a lookup to, so
-- something else must prove the caller is the donor. The reference alone cannot:
-- it travels in a callback URL, and treating it as sufficient would let anyone
-- who guesses or observes one read a stranger's donation.
--
-- Stored as a SHA-256 hash, like every other credential-bearing token in this
-- schema (S-018, S-024). The plaintext is returned once, at checkout, and is
-- sent in a header thereafter -- never a query string, because
-- internal/api/middleware/logger.go logs req.URL.String() unconditionally, so a
-- token in a query string would land in the request log, proxy logs and Referer
-- headers.
-- +migrate Up
ALTER TABLE donations
    ADD COLUMN access_token_hash bytea;

CREATE INDEX idx_donations_access_token_hash ON donations USING btree (access_token_hash);

-- +migrate Down
ALTER TABLE donations
    DROP COLUMN access_token_hash;

