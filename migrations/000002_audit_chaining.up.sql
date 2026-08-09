-- agro-iam — slice 3: tamper-evident, per-tenant audit chaining.
-- Adds seq / prev_hash / chain_hash / severity to app.audit_log. sha256(bytea)
-- is a core builtin since PG 11 — no extension, so the non-superuser
-- integration role (agroiam_rls) can apply this file. Deviation from DECISIONS
-- 2.3: the single-column id PK is kept — audit_log is never FK-referenced, so
-- the composite-PK side channel does not apply.
--
-- There is deliberately NO SQL backfill of chain hashes for pre-existing rows:
-- chain_hash is computed by Go over CanonicalizeEntry bytes (compact,
-- byte-wise sorted JSON, float64-safe) and an RFC3339Nano timestamp with
-- trailing zeros trimmed. Postgres jsonb::text rendering (spaces, key order,
-- numeric scale), fixed-width .US formatting and concat() skipping NULLs
-- cannot reproduce those bytes for arbitrary JSON, so any hash written by SQL
-- would be reported as tamper by VerifyChain — worse than failing loudly. The
-- migration therefore FAILS CLOSED when legacy rows exist; the operator must
-- re-chain them from the application (or empty the table) before applying.

ALTER TABLE app.audit_log
    ADD COLUMN seq        bigint,
    ADD COLUMN prev_hash  text,
    ADD COLUMN chain_hash text,
    ADD COLUMN severity   text NOT NULL DEFAULT 'info';

-- Fail closed: pre-existing rows cannot be chain-hashed in SQL (see header).
-- In practice the table is empty here — the pre-fix Append ran without tenant
-- context, so RLS FORCE rejected every insert (see audit_integration_test.go:
-- TestAuditChainingAppendWithoutTenantRejected) — the guard is belt and
-- suspenders against manual/superuser inserts.
DO $$
DECLARE
    legacy_count bigint;
BEGIN
    SELECT count(*) INTO legacy_count FROM app.audit_log;
    IF legacy_count > 0 THEN
        RAISE EXCEPTION
            'audit_log has % pre-chaining row(s); SQL cannot reproduce Go''s canonical chain hashes. Re-chain them from the application or empty the table before applying 000002.',
            legacy_count;
    END IF;
END $$;

ALTER TABLE app.audit_log
    ALTER COLUMN seq        SET NOT NULL,
    ALTER COLUMN prev_hash  SET NOT NULL,
    ALTER COLUMN chain_hash SET NOT NULL;

ALTER TABLE app.audit_log
    ADD CONSTRAINT uq_audit_log_tenant_seq UNIQUE (tenant_id, seq); -- serves tail read + verify ordering
