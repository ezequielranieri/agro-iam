# DECISIONS.md — Project constitution for `agro-iam`

🇪🇸 [Versión en español](./DECISIONS.es.md)

This file is the source of truth for any agent (human or AI) working on this
repository. Every implementation must respect these rules. If a task conflicts
with something defined here, **stop and consult before proceeding**.

Each decision records: the rule (what the code MUST do), the alternatives
rejected and why, and the cost the project accepts for choosing it. This last
part matters: knowing what a decision costs is what lets you reopen it later
with a strong justification instead of a hunch.

Status legend: `Accepted` = settled; `Open` = proposal, do not implement.

---

## 1. Tech stack (fixed, non-negotiable without explicit discussion)

- **Language:** Go 1.26+ (`go 1.26` in `go.mod`; CI pins `1.26.x`).
- **Persistence:** PostgreSQL 16 (`postgres:16-alpine`). It is the source of
  truth, and **Row Level Security (RLS) is the tenancy mechanism** — the
  database enforces isolation, not the application.
- **Redis:** Redis 7 (`redis:7-alpine`). Used **only for the client factory**
  (`internal/infrastructure/redis`) today — **NOT required for core flows**:
  startup logs a warning and keeps serving if Redis is down. This is a wiring
  placeholder, not a cache dependency; no core path reads from Redis yet. Slice 3
  (rate limiting, breach detection) is where Redis starts earning its keep.
- **Database access:** `pgx/v5` direct — `pgxpool` for connections, `pgx.Tx`
  for tenant-bound transactions. No ORM.
- **HTTP:** stdlib `net/http` `http.ServeMux` with Go 1.22+ pattern routing
  (`GET /api/v1/lots`, `{id}` path parameters). No chi/gin/echo.
- **Logging:** stdlib `log/slog`. No zerolog/zap/logrus.
- **Auth primitives** (`internal/infrastructure/auth`): JWT HS256
  (`golang-jwt/jwt/v5`), Argon2id (`golang.org/x/crypto/argon2`), opaque
  SHA-256 refresh-token families.
- **Migrations:** `golang-migrate`, versioned SQL in `migrations/`
  (migrate container in Docker Compose).
- **Configuration:** plain `os.Getenv` in `cmd/api/main.go` — deliberately no
  external config library.
- **Testing:** stdlib `go test` — unit tests (no DB) plus real-PostgreSQL
  integration tests that skip themselves unless `TEST_DATABASE_URL` is set;
  CI runs them against a `postgres:16-alpine` service container.
- **Local infra:** Docker Compose (`postgres:16-alpine` + `redis:7-alpine` +
  migrate container).

**Why these choices (and what was rejected)**
- **RLS over schema-per-tenant / database-per-tenant**: the product targets
  *many small tenants* sharing one database instance. RLS gives structural
  isolation that survives raw SQL and buggy queries, with one schema, one
  migration chain and simple backups (see 2.1).
- **`pgx/v5` over an ORM**: RLS depends on precise control of `set_config`,
  transaction boundaries, composite keys and raw policies — the exact features
  ORMs hide or fight (see 2.10).
- **stdlib `ServeMux` + `slog` over chi/gin/echo + zerolog/zap/logrus**:
  zero third-party dependencies for routing and logging; behavior matches the
  standard library's guarantees; the middleware stack stays small and
  reviewable (see 2.11).
- **Argon2id over bcrypt**: bcrypt resists ASIC/GPU attacks poorly because its
  memory footprint is tiny; Argon2id is memory-hard (see 2.8).
- **HS256 over RS256 for now**: symmetric simplicity with one shared secret;
  RS256/JWKS is the documented path when multi-service verification is needed
  (see 2.6).
- **Modular monolith over microservices**: the domain is small; microservices
  would fragment the tenancy model, RLS session handling and refresh-token
  rotation across process boundaries (see 2.12).
- **Redis is a placeholder, not a dependency**: only the client factory exists
  today; it is wired now so `cmd/api` does not change later, but no core flow
  depends on it — a deliberate difference from a cache dependency.

---

## 2. Confirmed design decisions (don't reopen without strong justification)

> Status of every subsection: Accepted — do not reopen without strong justification (see preamble).

### 2.1 RLS as the tenancy enforcement mechanism — Status: Accepted

**Rule**
- Tenancy MUST be enforced by **PostgreSQL Row Level Security**, never by the
  application layer alone. Isolation MUST hold against a buggy query, a broken
  repository layer, or a raw `psql` session.
- Every tenanted table MUST carry `tenant_id`, MUST have RLS enabled, and MUST
  have a policy filtering rows on `tenant_id = app.current_tenant_id()`.
- `app.tenants` (cooperative registry) and `app.roles` (shared role catalog)
  are **global**: neither carries `tenant_id`, neither has RLS, and neither may
  be tenanted.
- The application MUST bind the tenant context per transaction (see 2.4) and
  let the database decide which rows are visible.

**Alternatives rejected**
- *Schema-per-tenant*: hundreds of small tenants would explode the catalog and
  the migration/backup tooling; the product needs cheap per-tenant operations.
- *Database-per-tenant*: per-tenant dedicated infrastructure must be cheap to
  operate; one instance serves thousands of small tenants.
- *Application-layer filtering only* (`WHERE tenant_id = ...`): not structural —
  a buggy query or a raw SQL session bypasses it; isolation must survive
  application bugs.

**Cost accepted**
- Every query needs a tenant context or it silently returns zero rows — a
  missing GUC yields a NULL predicate that is always false, so debugging an
  "empty" result requires knowing the GUC.
- Postgres-only mechanism: RLS has no portable equivalent, so the database
  choice is locked in.
- An unbound connection can confuse developers who expect a global view.

### 2.2 FORCE RLS on all tenanted tables — Status: Accepted

**Rule**
- Every tenanted table MUST be `ALTER TABLE ... FORCE ROW LEVEL SECURITY` in
  addition to `ENABLE`. FORCE extends RLS to the table owner, so any normal
  role is constrained; only superusers bypass RLS, by design.
- Applies to: `app.users`, `app.user_roles`, `app.lots`, `app.campaigns`,
  `app.applications`, `app.audit_log`.

**Alternatives rejected**
- *ENABLE RLS without FORCE*: a table owner — often the migration user or a
  DBA — bypasses RLS entirely, so an accidentally unfiltered query from the
  application's connection could cross tenants.
- *Trusting the application to always filter by tenant*: the point of FORCE is
  defense-in-depth against a raw `psql` session or a DBA misstep, not just a
  well-behaved application layer.

**Cost accepted**
- Migration and seeding operations that must touch every tenant (e.g. global
  backfills) require elevated privileges.
- The integration harness needs a dedicated non-superuser role (`agroiam_rls`)
  because a superuser connection "passes" RLS assertions vacuously —
  superusers always bypass RLS.

### 2.3 Composite primary keys (id, tenant_id) — Status: Accepted

**Rule**
- Tenanted business tables MUST use a **composite primary key `(id, tenant_id)`**
  — as in `app.lots`, `app.campaigns`, `app.applications`.
- A foreign key or `JOIN` referencing a tenanted row MUST include the tenant:
  a referencing key cannot point at another tenant's row without also naming
  that tenant.

**Alternatives rejected**
- *Single-column `id` primary key*: creates a side channel — a foreign key on
  an unprotected table, or any `JOIN`, can reference another tenant's row by
  its plain `id`, and a query that returns that id reveals that a row exists
  even if RLS hides its columns.

**Cost accepted**
- `FOREIGN KEY` references to tenanted tables must repeat the composite column
  pair.
- ORMs and tooling that assume a single-column primary key need adaptation —
  agro-iam avoids ORMs (see 2.10).

### 2.4 Tenant context bound via set_config per transaction — Status: Accepted

**Rule**
- The tenant context MUST be bound with
  `SELECT set_config('app.tenant_id', $1, true)` inside a **dedicated
  transaction** — the `true` makes the setting LOCAL to that transaction.
  Never a plain `SET` on a pooled connection.
- Every tenant-scoped unit of work MUST run through the `WithTenant` helper in
  `internal/infrastructure/postgres/db.go`, which begins the transaction, sets
  the GUC, runs the unit of work against the transaction, and commits (or rolls
  back) as one atomic boundary.

**Alternatives rejected**
- *Plain `SET` on a pooled connection*: the setting persists on that connection
  after the unit of work ends; the pool then reuses it for tenant B's queries
  while it still carries tenant A's identity — leaking tenant A's context into
  tenant B's requests.
- *Setting the GUC outside a transaction (ad-hoc scripts)*: no atomic boundary,
  and the classic foot-gun for future contributors.

**Cost accepted**
- Every tenant-scoped unit of work pays the cost of a transaction.
- Setting the GUC outside the `WithTenant` helper is the main foot-gun for
  future contributors; a whole unit of work observes a single tenant context
  (no cross-tenant reads within one transaction).

### 2.5 Login contract includes tenant_id (realm pattern) — Status: Accepted

**Rule**
- The login contract MUST be `{email, password, tenant_id}`: the application
  binds the tenant FIRST, and only then resolves the user. This is the realm
  pattern used by Auth0/Cognito, where the tenant (realm) is part of the
  credential exchange.
- Any failure MUST return a uniform **401**, indistinguishable whether the
  email, password or tenant was wrong.
- Rationale: RLS is FORCED on `app.users` (see 2.2), so a user row is invisible
  unless the session is bound to that user's tenant — a login that looks the
  user up by email first returns nothing before the tenant is known, and the
  tenant cannot be derived from the user without breaking isolation.

**Alternatives rejected**
- *Look up the user by email, then check the tenant*: the lookup returns
  nothing before the tenant is known; the tenant cannot be derived from the
  user without breaking isolation.
- *Differentiated error responses*: would leak which component of the
  credentials (email, password, tenant) failed.

**Cost accepted**
- The client must know its tenant identifier up front; a lost `tenant_id`
  cannot be recovered through the API.
- The API surface differs from single-tenant login conventions.
- A uniform 401 means a client cannot tell which field to fix from the response
  alone.

### 2.6 Short-lived HS256 JWTs with pinned algorithm — Status: Accepted

**Rule**
- Access tokens MUST be **HS256 JWTs** with a **15-minute TTL** by default
  (`accessTokenTTL` in `cmd/api/main.go`), carrying `sub` (user id),
  `tenant_id` and `role` claims.
- The algorithm MUST be **pinned to HS256**: verification MUST reject any other
  signing method, closing the classic `alg=none` and key-confusion attacks.

**Alternatives rejected**
- *Opaque session tokens (server-side state)*: no revocation problem, but they
  need per-request server state; stateless tokens were chosen for simplicity.
- *RS256 (asymmetric)*: more key-management moving parts; rejected for now in
  favor of implementation simplicity, with a documented path to RS256/JWKS when
  multi-service verification is needed.

**Cost accepted**
- HS256 is symmetric — the secret both signs and verifies; a leaked
  `JWT_SECRET` forges tokens for any tenant, so the secret must be rotated on a
  schedule.
- Access tokens cannot be revoked before expiry (mitigated by the short TTL).
- A second service can only verify tokens it shares the secret with.

### 2.7 Opaque refresh tokens stored as SHA-256 with family rotation — Status: Accepted

**Rule**
- Refresh tokens MUST be **opaque 256-bit random values** (`crypto/rand`,
  base64url) returned to the client exactly once and persisted **only as their
  SHA-256 digest** (`internal/application/services/refresh.go`). Default TTL:
  30 days (`refreshTokenTTL` in `cmd/api/main.go`).
- Rotation MUST keep a **per-family chain**: every refresh revokes the old
  token, issues a successor in the same `family_id`, and records `replaced_by`.
- Presenting an already-rotated token (revoked with `replaced_by` set) IS the
  replay signature and MUST revoke the **whole family**
  (`RevokeFamily`), killing the attacker's freshly issued successor too.

**Alternatives rejected**
- *Store plaintext refresh tokens*: a database breach would let an attacker
  replay the token forever.
- *Rotation without family tracking*: in the classic race where both the
  attacker and the legitimate user present the same token, whoever is second
  must not succeed — without family-wide revocation the attacker's stolen
  successor survives.

**Cost accepted**
- Rotation adds state and concurrency-sensitive writes.
- A lost or untransmitted successor after a refresh forces a re-login.
- Replay of an *expired* token must not be misclassified as theft — rotation
  logic must distinguish Expired from Revoked.

### 2.8 Argon2id password hashing over bcrypt — Status: Accepted

**Rule**
- Passwords MUST be hashed with **Argon2id** using the OWASP interactive
  parameters `m=65536, t=1, p=4` (64 MiB memory, 1 iteration, 4 lanes), a
  32-byte key, and a random 16-byte salt.
- Hashes MUST be encoded in the **PHC format**
  (`$argon2id$v=19$m=65536,t=1,p=4$...`), and `Verify` MUST re-derive the
  parameters from the stored string itself — never from configuration — so a
  future parameter bump is transparent to existing hashes.

**Alternatives rejected**
- *bcrypt*: its memory footprint is tiny, so it resists ASIC/GPU brute force
  poorly by design; the community recommendation for interactive logins has
  moved on.

**Cost accepted**
- Hashing costs 64 MiB per call — more CPU/memory per login.
- Not a FIPS-approved primitive in some compliance contexts.
- Parameters are fixed in code, so re-hashing on a parameter change is a
  migration.

### 2.9 app.refresh_tokens deliberately not RLS-protected — Status: Accepted

**Rule**
- `app.refresh_tokens` MUST **not** be RLS-protected. Lookup is keyed by the
  unguessable token digest (see 2.7), and each row MUST carry its own
  `tenant_id` so the flow can restore the tenant context after a successful
  rotation.
- The threat model is shifted: the token *is* the capability, and the
  cryptographic strength of the hash is what protects the table.

**Alternatives rejected**
- *Protect `app.refresh_tokens` with RLS*: at refresh time the access token is
  (usually) expired, so the client cannot supply a tenant the application can
  trust as its own — a refresh lookup would be impossible without
  reintroducing the very side-channel the design avoids.

**Cost accepted**
- The table is readable by any role with table-level SELECT — its security
  rests entirely on the unguessability of the stored hashes.
- A full-table dump is sensitive even though it cannot be replayed.

### 2.10 Direct pgx/v5 without an ORM — Status: Accepted

**Rule**
- All Postgres access MUST use **`pgx/v5` directly** — `pgxpool` for
  connections, `pgx.Tx` for tenant-bound transactions — with hand-written SQL
  for every query. No ORM (no GORM, no sqlc's generated-code path, no ent).

**Alternatives rejected**
- *ORM (GORM, sqlc codegen, ent)*: ORMs commonly assume single-column keys,
  manage their own connection settings, and generate SQL that bypasses tenant
  context — they hide or fight `set_config`, `WithTenant` boundaries and
  composite primary keys (see 2.3, 2.4).

**Cost accepted**
- Every query is written and reviewed by hand; no automatic mapping, so struct
  scanning is explicit.
- Schema (migrations) and queries must be kept in sync manually.

### 2.11 stdlib http.ServeMux and slog — Status: Accepted

**Rule**
- HTTP routing MUST use the **stdlib `net/http` `http.ServeMux`** with Go 1.22+
  pattern routing (`GET /api/v1/lots`, `{id}` path parameters) and the **stdlib
  `log/slog`** structured logger.
- Middleware (logging, recovery, request-id, `RequireAuth`) MUST be plain
  `http.Handler` wrappers. chi/gin/echo and zerolog/zap/logrus are not used.

**Alternatives rejected**
- *chi / gin / echo*: third-party routers with their own conventions for
  middleware, path parameters and handlers — dependencies with nothing to fill.
- *zerolog / zap / logrus*: third-party loggers; `slog` covers structured
  logging.

**Cost accepted**
- stdlib routing has a smaller feature set than chi/gin (no middleware chains,
  no per-route middleware groups — handlers compose explicitly).
- No structured-logging features beyond slog's core.
- Pattern routing requires Go 1.22+.

### 2.12 Modular monolith with clean/hexagonal layering — Status: Accepted

**Rule**
- The system MUST be a **modular monolith** with clean/hexagonal layering:
  - `internal/domain` — pure entities, zero external dependencies (stdlib only);
  - `internal/application` — use cases depending only on `ports` interfaces
    (DIP);
  - `internal/infrastructure` — adapters: `postgres`, `redis`, `auth`;
  - `internal/http` — stdlib transport;
  - composed in `cmd/api` (composition root).
- Microservices are rejected.

**Alternatives rejected**
- *Microservices*: they would fragment the tenancy model, RLS session handling
  and the refresh-token rotation logic across process boundaries, and introduce
  network failure modes, deployment tooling and distributed testing before
  there is a need.

**Cost accepted**
- A monolith scales by vertical scaling or replication, not by independent
  service deployment.
- One process holds all of the security surface — a compromise affects
  everything.
- Discipline is required to keep the layers honest as the codebase grows.

---

## 3. Code conventions

- **Domain is pure**: `internal/domain` imports nothing but the standard
  library — entities and sentinel errors, zero external dependencies.
- **SQL lives in two places only**: `migrations/` and
  `internal/infrastructure/postgres`. SQL is never called directly from
  `internal/http` handlers or `internal/application` services.
- **All tenant-scoped access goes through the `WithTenant` helper**
  (`internal/infrastructure/postgres/db.go`); repositories run every query
  inside it so RLS is the enforcement point, not a `WHERE` clause.
- **Errors are mapped at the adapter boundary**: pgx driver errors are
  translated into domain errors (`mapPgErr` — e.g. `unique_violation` 23505 →
  `domain.ErrConflict`); application code never depends on pgx error types.
- **Composite-key awareness**: tenanted tables are keyed `(id, tenant_id)`;
  foreign keys repeat the composite pair.
- **Naming**: tables and columns in `snake_case`; Go types and functions in
  idiomatic style (`CamelCase` / `camelCase`).
- **Comments are written in English** (EN).
- **Configuration**: plain `os.Getenv` — no external config library.

---

## 4. Explicit prohibitions

- Don't disable, weaken, or bypass FORCE RLS on any tenanted table; don't run
  tenant-scoped queries outside the `WithTenant` helper.
- Don't add an ORM (GORM, sqlc codegen, ent, or similar) — hand-written pgx SQL
  only.
- Don't add chi, gin, echo, or zerolog/zap/logrus — stdlib `ServeMux` and
  `slog` only.
- Don't store plaintext refresh tokens or plaintext passwords — SHA-256 digests
  and Argon2id PHC strings only.
- Don't allow JWT algorithm flexibility — HS256 is pinned; verification rejects
  any other method (no `alg=none`, no key confusion).
- Don't add `tenant_id` to the global tables `app.tenants` and `app.roles` —
  they are global by design.
- Don't add RLS to `app.refresh_tokens` — it is deliberately unprotected (see
  2.9).
- Don't introduce schema-per-tenant or database-per-tenant — RLS on one shared
  instance is the tenancy mechanism (see 2.1).

---

## 5. Reference roadmap (high level)

From the README (slices 0–5 as currently listed):

- [x] **Slice 0** — skeleton: schema + RLS, repos, auth primitives, HTTP shell
- [x] **Slice 1** — JWT auth middleware, protected lot endpoints with real RLS
  isolation, tenant-context claims
- [x] **Slice 2** — RLS isolation integration tests against live PostgreSQL
  (dedicated non-superuser role) + GitHub Actions CI
- [ ] **Slice 3** — audit event emission with hash-chaining, Redis-backed rate
  limiting, breach detection / token-compromise alerting
- [ ] **Slice 4** — campaigns & applications CRUD over the RLS repos, user
  provisioning, RBAC enforcement
- [ ] **Slice 5** — demo frontend / deployed demo

---

## How to add a decision

1. Pick the next free number (`### 2.13`, ...) in section 2.
2. Copy the template below, fill it in, and keep the section ordered.

```markdown
### 2.N <Title> — Status: Accepted

**Rule**
- <what the code MUST do, as MUST-style bullets>

**Alternatives rejected**
- *<alternative>*: <technical reason it was rejected>

**Cost accepted**
- <honest trade-off the project accepts>
```

3. Update the Spanish mirror in `DECISIONS.es.md` in the same session so both
   languages stay in sync.
