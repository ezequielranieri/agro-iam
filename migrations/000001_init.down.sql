-- agro-iam — slice 0 rollback. Order matters: RLS-protected tables first, then
-- the catalog, then the schema-level objects.

DROP POLICY IF EXISTS tenant_isolation ON app.audit_log;
DROP POLICY IF EXISTS tenant_isolation ON app.applications;
DROP POLICY IF EXISTS tenant_isolation ON app.campaigns;
DROP POLICY IF EXISTS tenant_isolation ON app.lots;
DROP POLICY IF EXISTS tenant_isolation ON app.user_roles;
DROP POLICY IF EXISTS tenant_isolation ON app.users;

ALTER TABLE app.audit_log    DISABLE ROW LEVEL SECURITY;
ALTER TABLE app.applications DISABLE ROW LEVEL SECURITY;
ALTER TABLE app.campaigns    DISABLE ROW LEVEL SECURITY;
ALTER TABLE app.lots         DISABLE ROW LEVEL SECURITY;
ALTER TABLE app.user_roles   DISABLE ROW LEVEL SECURITY;
ALTER TABLE app.users        DISABLE ROW LEVEL SECURITY;

DROP TABLE IF EXISTS app.refresh_tokens;
DROP TABLE IF EXISTS app.audit_log;
DROP TABLE IF EXISTS app.applications;
DROP TABLE IF EXISTS app.campaigns;
DROP TABLE IF EXISTS app.lots;
DROP TABLE IF EXISTS app.user_roles;
DROP TABLE IF EXISTS app.users;
DROP TABLE IF EXISTS app.roles;
DROP TABLE IF EXISTS app.tenants;

DROP FUNCTION IF EXISTS app.current_tenant_id();

DROP SCHEMA IF EXISTS app;
