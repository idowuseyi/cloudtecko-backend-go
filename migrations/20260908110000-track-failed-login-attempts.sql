-- Track consecutive failed logins per account.
--
-- NIST SP 800-63B Rev 4 requires verifiers to limit consecutive failed
-- authentication attempts on an account. The existing rate limiter is per-IP,
-- which does not help here: an attacker distributing attempts across addresses
-- walks straight past it while hammering a single account.
--
-- This is a SOFT lockout on purpose. The threshold is generous and the window is
-- short, because a hard lockout hands an attacker a denial-of-service primitive:
-- anyone who knows a username could keep that user permanently locked out.
-- Bounding the window to minutes keeps the brute-force cost high while keeping
-- the abuse cost low.
-- +migrate Up
ALTER TABLE users
    ADD COLUMN failed_login_attempts int NOT NULL DEFAULT 0;

ALTER TABLE users
    ADD COLUMN last_failed_login_at timestamptz;

-- +migrate Down
ALTER TABLE users
    DROP COLUMN failed_login_attempts;

ALTER TABLE users
    DROP COLUMN last_failed_login_at;

