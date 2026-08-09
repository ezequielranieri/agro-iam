-- agro-iam — slice 3 rollback: drop only the chaining columns and the
-- per-tenant sequence constraint. Audit rows themselves are kept.

ALTER TABLE app.audit_log DROP CONSTRAINT IF EXISTS uq_audit_log_tenant_seq;
ALTER TABLE app.audit_log
    DROP COLUMN seq, DROP COLUMN prev_hash, DROP COLUMN chain_hash, DROP COLUMN severity;
