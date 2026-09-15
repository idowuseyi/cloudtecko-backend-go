-- An order's access token, mirroring what donations already carry.
--
-- Guest checkout is supported, so there is no account to scope a lookup to.
-- The reference cannot stand in for one: it travels in the payment provider's
-- callback URL, so treating it as proof would let anyone who observes one read
-- a stranger's order.
--
-- Hashed at rest, returned once at checkout, and sent in a header thereafter --
-- never a query string, because the request logger records the full URL
-- unconditionally.
-- +migrate Up
ALTER TABLE orders
    ADD COLUMN access_token_hash bytea;

CREATE INDEX idx_orders_access_token_hash ON orders USING btree (access_token_hash);

-- +migrate Down
ALTER TABLE orders
    DROP COLUMN access_token_hash;

