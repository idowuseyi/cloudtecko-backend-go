-- An audit trail for admin actions.
--
-- An admin can approve applications, refund payments, edit prices and cancel
-- orders. Without this, none of that leaves a trace, and after the fact there is
-- no way to reconstruct who did what -- which matters most exactly when
-- something has gone wrong and money is involved.
--
-- before/after are jsonb snapshots rather than a diff, because a diff is only
-- interpretable against a schema that may since have changed.
--
-- actor_user_id is ON DELETE SET NULL, like every other reference to users:
-- DeleteUserAccount relies on cascades, and an erasure request must not be able
-- to erase the record of what that account DID.
-- +migrate Up
CREATE TABLE admin_audit_log (
    id uuid NOT NULL DEFAULT uuid_generate_v4 (),
    actor_user_id uuid,
    -- Retained even when the actor row is gone, so an erased account still has a
    -- readable trail.
    actor_email text NOT NULL DEFAULT '',
    action text NOT NULL,
    subject_type text NOT NULL,
    subject_id text NOT NULL DEFAULT '',
    before jsonb,
    after jsonb,
    source_ip_prefix text,
    created_at timestamptz NOT NULL,
    CONSTRAINT admin_audit_log_pkey PRIMARY KEY (id)
);

CREATE INDEX idx_admin_audit_log_fk_actor_user_id ON admin_audit_log USING btree (actor_user_id);

CREATE INDEX idx_admin_audit_log_created_at ON admin_audit_log USING btree (created_at DESC);

CREATE INDEX idx_admin_audit_log_subject ON admin_audit_log USING btree (subject_type, subject_id);

ALTER TABLE admin_audit_log
    ADD CONSTRAINT admin_audit_log_actor_user_id_fkey FOREIGN KEY (actor_user_id) REFERENCES users (id) ON UPDATE CASCADE ON DELETE SET NULL;

-- +migrate Down
DROP TABLE IF EXISTS admin_audit_log;

