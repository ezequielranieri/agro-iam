-- agro-iam — slice 0 initial schema + Row Level Security.
--
-- Tenancy model:
--   * app.tenants is the public registry of organizations (cooperatives /
--     producer groups). NOT RLS-protected: it must be readable to resolve a
--     tenant before any tenant-scoped work.
--   * app.roles is a global catalog shared by all tenants. NOT RLS-protected.
--   * everything else carries tenant_id and is RLS-protected AND FORCED.
--
-- RLS mechanism: the application begins a transaction and runs
--   SELECT set_config('app.tenant_id', $1, true)
-- (true = LOCAL to the transaction only — never plain SET on a pooled
-- connection, which would leak tenant A's identity into tenant B's queries).
-- Every RLS policy compares the row's tenant_id against
-- app.current_tenant_id(), which reads that GUC. When the GUC is NULL (no
-- tenant context), current_tenant_id() returns NULL and every policy predicate
-- is false: a missing context yields zero rows, never a leak.

CREATE SCHEMA IF NOT EXISTS app;

-- ---------------------------------------------------------------------------
-- GUC helpers
-- ---------------------------------------------------------------------------

-- Returns the tenant id bound to the current transaction, or NULL when no
-- tenant context has been established.
CREATE OR REPLACE FUNCTION app.current_tenant_id()
RETURNS uuid
LANGUAGE sql
STABLE
AS $$
  SELECT NULLIF(current_setting('app.tenant_id', true), '')::uuid
$$;

-- ---------------------------------------------------------------------------
-- Catalog / registry tables (no RLS)
-- ---------------------------------------------------------------------------

CREATE TABLE app.tenants (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name       text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE app.roles (
    code        text PRIMARY KEY,
    name        text NOT NULL,
    description text
);

-- ---------------------------------------------------------------------------
-- Tenanted tables (RLS)
-- ---------------------------------------------------------------------------

CREATE TABLE app.users (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id     uuid NOT NULL REFERENCES app.tenants(id),
    email         text NOT NULL UNIQUE,      -- globally unique: one account per person
    password_hash text NOT NULL,
    full_name     text NOT NULL,
    is_active     boolean NOT NULL DEFAULT true,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE app.user_roles (
    user_id   uuid NOT NULL REFERENCES app.users(id) ON DELETE CASCADE,
    role_code text NOT NULL REFERENCES app.roles(code),
    tenant_id uuid NOT NULL REFERENCES app.tenants(id),
    PRIMARY KEY (user_id, role_code)
);

-- Composite primary keys on tenanted business tables: the tenant_id half makes
-- it impossible to JOIN or reference a row of another tenant even if an
-- attacker crafts a foreign tenant's uuid. PK id alone would let RLS leak a
-- row through a foreign key from a table without the same policy.
CREATE TABLE app.lots (
    id         uuid NOT NULL DEFAULT gen_random_uuid(),
    tenant_id  uuid NOT NULL REFERENCES app.tenants(id),
    name       text NOT NULL,
    area_ha    numeric(10,2),
    crop       text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (id, tenant_id)
);

CREATE TABLE app.campaigns (
    id         uuid NOT NULL DEFAULT gen_random_uuid(),
    tenant_id  uuid NOT NULL REFERENCES app.tenants(id),
    name       text NOT NULL,
    season     text,
    started_at date,
    ended_at   date,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (id, tenant_id)
);

CREATE TABLE app.applications (
    id           uuid NOT NULL DEFAULT gen_random_uuid(),
    tenant_id    uuid NOT NULL REFERENCES app.tenants(id),
    lot_id       uuid NOT NULL,
    campaign_id  uuid NOT NULL,
    product_name text NOT NULL,
    dose         text,
    applied_at   timestamptz NOT NULL DEFAULT now(),
    operator_id  uuid REFERENCES app.users(id),
    notes        text,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (id, tenant_id)
);

CREATE TABLE app.audit_log (
    id            bigserial PRIMARY KEY,
    tenant_id     uuid NOT NULL REFERENCES app.tenants(id),
    actor_user_id uuid REFERENCES app.users(id),
    action        text NOT NULL,
    entity_type   text NOT NULL,
    entity_id     text,
    payload       jsonb,
    created_at    timestamptz NOT NULL DEFAULT now()
);

-- Refresh token families. NOT RLS-protected by design: rotation lookup happens
-- by opaque token hash, which cannot be guessed, and the tenant_id is carried
-- inside each record so the flow can restore tenant context on refresh.
CREATE TABLE app.refresh_tokens (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     uuid NOT NULL REFERENCES app.users(id) ON DELETE CASCADE,
    tenant_id   uuid NOT NULL REFERENCES app.tenants(id),
    family_id   uuid NOT NULL,
    token_hash  text NOT NULL UNIQUE,
    expires_at  timestamptz NOT NULL,
    revoked_at  timestamptz,
    replaced_by uuid,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_refresh_tokens_family ON app.refresh_tokens (family_id);

-- ---------------------------------------------------------------------------
-- Row Level Security
-- ---------------------------------------------------------------------------

-- ENABLE + FORCE: FORCE additionally applies RLS to the table owner, so even a
-- DBA session cannot bypass isolation without superuser rights. RLS is the
-- last line of defense against a buggy query or a raw psql session that forgets
-- to filter by tenant.
ALTER TABLE app.users        ENABLE ROW LEVEL SECURITY;
ALTER TABLE app.users        FORCE ROW LEVEL SECURITY;
ALTER TABLE app.user_roles   ENABLE ROW LEVEL SECURITY;
ALTER TABLE app.user_roles   FORCE ROW LEVEL SECURITY;
ALTER TABLE app.lots         ENABLE ROW LEVEL SECURITY;
ALTER TABLE app.lots         FORCE ROW LEVEL SECURITY;
ALTER TABLE app.campaigns    ENABLE ROW LEVEL SECURITY;
ALTER TABLE app.campaigns    FORCE ROW LEVEL SECURITY;
ALTER TABLE app.applications ENABLE ROW LEVEL SECURITY;
ALTER TABLE app.applications FORCE ROW LEVEL SECURITY;
ALTER TABLE app.audit_log    ENABLE ROW LEVEL SECURITY;
ALTER TABLE app.audit_log    FORCE ROW LEVEL SECURITY;

-- Each policy is identical in shape: a row is visible and writable only when
-- its tenant_id matches the tenant bound to the current transaction.
-- USING guards SELECT/UPDATE/DELETE, WITH CHECK guards INSERT/UPDATE.
CREATE POLICY tenant_isolation ON app.users        USING (tenant_id = app.current_tenant_id()) WITH CHECK (tenant_id = app.current_tenant_id());
CREATE POLICY tenant_isolation ON app.user_roles   USING (tenant_id = app.current_tenant_id()) WITH CHECK (tenant_id = app.current_tenant_id());
CREATE POLICY tenant_isolation ON app.lots         USING (tenant_id = app.current_tenant_id()) WITH CHECK (tenant_id = app.current_tenant_id());
CREATE POLICY tenant_isolation ON app.campaigns    USING (tenant_id = app.current_tenant_id()) WITH CHECK (tenant_id = app.current_tenant_id());
CREATE POLICY tenant_isolation ON app.applications USING (tenant_id = app.current_tenant_id()) WITH CHECK (tenant_id = app.current_tenant_id());
CREATE POLICY tenant_isolation ON app.audit_log    USING (tenant_id = app.current_tenant_id()) WITH CHECK (tenant_id = app.current_tenant_id());

-- ---------------------------------------------------------------------------
-- Seed data: the role vocabulary of the agricultural workflow.
-- ---------------------------------------------------------------------------
INSERT INTO app.roles (code, name, description) VALUES
    ('admin',      'Admin',       'Cooperative manager with full access'),
    ('producer',   'Producer',    'Field owner (productor)'),
    ('agronomist', 'Agronomist',  'Crop advisor (agrónomo)'),
    ('auditor',    'Auditor',     'Compliance and review access'),
    ('hauler',     'Hauler',      'Transportista');
