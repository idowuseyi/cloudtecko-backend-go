-- Adds an 'admin' scope, which guards the /api/v1/admin/* surface (content CRUD,
-- membership review, order fulfilment, refunds).
--
-- This DROPs and re-CREATEs the type rather than using ALTER TYPE ... ADD VALUE.
-- ADD VALUE is the usual advice, but it is irreversible -- Postgres has no DROP
-- VALUE -- and cannot run in the same transaction as its first use, so it could
-- not be given the Down migration this repository requires. Re-creating is safe
-- here specifically because NO column is of type user_scope: users.scopes is
-- text[] (see 20200428072232-create-users.sql:7, which records why), so the type
-- has no dependents and dropping it cannot cascade. If a column ever adopts this
-- type, this technique stops being available and ADD VALUE becomes the only route.
--
-- Authorisation semantics: middleware.AuthConfig.CheckUserScopes is OR, not AND
-- (internal/api/middleware/auth.go), so the admin route group must list 'admin'
-- ALONE. Listing 'app' alongside it would admit every authenticated user.
-- +migrate Up
DROP TYPE IF EXISTS user_scope;

CREATE TYPE user_scope AS ENUM (
    'app',
    'cms',
    'admin'
);

-- +migrate Down
DROP TYPE IF EXISTS user_scope;

CREATE TYPE user_scope AS ENUM (
    'app',
    'cms'
);

