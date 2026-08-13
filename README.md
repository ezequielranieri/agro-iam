# agro-iam

Multi-tenant Identity & Access Management platform for the agricultural sector.
A learning/portfolio project demonstrating **Clean / Hexagonal Architecture**,
**Row Level Security (RLS)** tenancy, and defense-in-depth auth (Argon2id +
JWT + rotating refresh-token families) in idiomatic Go.

[![ci](https://github.com/ezequielranieri/agro-iam/actions/workflows/ci.yml/badge.svg)](https://github.com/ezequielranieri/agro-iam/actions/workflows/ci.yml)

> [Español](./README.es.md)

> Documentation: [DECISIONS.md](./DECISIONS.md) — architecture decisions & project constitution

> Slice 0 — foundation skeleton: domain model, ports, repositories, auth
> primitives, RLS-backed schema, and the HTTP shell. End-to-end flows (user
> provisioning, campaign/application CRUD, RBAC enforcement) build on that
> foundation and are delivered through slice 4.

## The problem

A cooperative groups hundreds of producers, agronomists, haulers and auditors.
Each group's data is private — a producer in Cooperativa Río Solo must never
see the lots, applications or audit trail of Cooperativa Valle Norte. Agro-iam
makes that isolation structural: **the database enforces it**, not just the
application.

## Architecture

```
┌─────────────────────────────────────────────────────────────────────┐
│                          HTTP layer (stdlib)                        │
│   router.go · middleware.go (slog, recover, request-id) · handlers   │
└───────────────▲───────────────────────────────▲─────────────────────┘
                │                               │
   ports.AuthService              ports.{User,Tenant,Lot,}Repo
                │                    │ (outbound) BreachSignalSink
┌───────────────┴───────────────────────────────┴─────────────────────┐
│                  Application layer (use cases)                     │
│   services/auth_service.go — login, refresh (rotation + replay)     │
│   services/breach.go — Detect() → severitized events → fan-out      │
│   ports/repositories.go · ports/services.go   (DIP: depends on      │
│                                                  interfaces only)   │
└───────────────▲───────────────────────────────▲─────────────────────┘
                │                               │
┌───────────────┴───────────────────────────────┴─────────────────────┐
│                 Infrastructure layer (adapters)                    │
│   postgres/*  (pgx, RLS tenant sessions, audit chain rows)          │
│   auth/*      (JWT HS256 · Argon2id · refresh families)             │
│   redis/      (rate-limit Lua INCR+PEXPIRE · go-redis factory)      │
└─────────────────────────────────────────────────────────────────────┘

Domain layer (internal/domain): pure entities + sentinel errors, ZERO
dependencies — imports nothing but the standard library.
```

```
cmd/api (composition root)
internal/
├── domain/           entities + domain errors (no external deps)
├── application/
│   ├── ports/        interfaces the core depends on (DIP) — incl. BreachSignalSink
│   └── services/     use cases: auth_service, breach.go (Detect + emit fan-out), sinks
├── infrastructure/   adapters: postgres, redis, auth (JWT/Argon2id/refresh)
└── http/             stdlib ServeMux (Go 1.22 {params}), middleware, handlers
migrations/           SQL schema + RLS + seed
docker-compose.yml    postgres:16 + redis:7 + migrate
Makefile · .env.example · README.md
```

## Tenancy model & RLS

Tenants and roles are **global**: `app.tenants` is the registry of cooperatives;
`app.roles` is the shared role catalog. Neither carries `tenant_id` and neither
has RLS.

Every business table carries `tenant_id` and is RLS-protected **and FORCED**:

```sql
CREATE POLICY tenant_isolation ON app.lots
  USING (tenant_id = app.current_tenant_id())
  WITH CHECK (tenant_id = app.current_tenant_id());
```

- `app.current_tenant_id()` reads the `app.tenant_id` GUC, returning `NULL`
  when unset — and a NULL predicate is *always false*, so a missing tenant
  context yields **zero rows, never a leak**.
- The application binds the tenant **inside a transaction**:
  `SELECT set_config('app.tenant_id', $1, true)` — the `true` makes it LOCAL.
  Plain `SET` on a pooled connection would leak tenant A's identity into
  tenant B's reused connections; the transaction scoping prevents that.
- `FORCE` extends RLS to the table owner, so even a raw `psql` session or a
  buggy query cannot accidentally cross tenants without superuser rights.
- Tenanted tables use **composite primary keys** `(id, tenant_id)`. This
  closes the side channel where a foreign key from an unprotected table could
  reference (and therefore reveal) another tenant's row.
- `app.refresh_tokens` is deliberately **not** RLS-protected: rotation lookup
  is by opaque token hash (unguessable), and each row carries the `tenant_id`
  needed to restore tenant context on refresh.

## Security design

| Concern | Mechanism |
|---|---|
| Password storage | Argon2id (m=65536, t=1, p=4, 32-byte key), PHC-encoded, `Verify` parses the format — never trusts config |
| Access token | JWT HS256, `sub`/`tenant_id`/`role`, **15 min TTL**, algorithm pinned to HS256 |
| Refresh token | Opaque 256-bit random, stored as SHA-256 only |
| Rotation | Every refresh revokes the old token, issues a successor in the same `family_id` |
| Replay detection | Presenting an already-rotated (revoked + `replaced_by`) token revokes the **whole family** |
| Tenant isolation | PostgreSQL RLS as described above |
| Audit integrity | Append-only `app.audit_log` with per-tenant hash chaining (`seq`, `prev_hash`, `chain_hash`); `VerifyChain` detects tampering; security events carry a severity (info/warn/critical) |
| Rate limiting | Per-route fixed-window counters — Redis Lua `INCR+PEXPIRE` with an in-memory fail-open fallback; `429 + Retry-After`; login 5/min/IP, refresh 30/min/IP, lots 120/min/tenant:user |
| Breach signals | Table-driven `Detect()` classifies outcomes into severitized events emitted to slog + audit; outbound `ports.BreachSignalSink` fan-out for future consumers |

## Quickstart

Requires: Go 1.26+, Docker with Compose.

```bash
# 1. start db + redis, apply migrations
make up

# 2. configure the app
cp .env.example .env          # set DATABASE_URL, JWT_SECRET (never "change-me")

# 3. run the API
make run

# sanity check
curl http://localhost:8080/healthz   # -> {"status":"ok"}

# login (tenant id required — see README note on login below)
curl -X POST http://localhost:8080/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"producer@coop-a.example","password":"s3cret","tenant_id":"<uuid>"}'
```

Other targets: `make down` (stop), `make demo` (up + seed + run, one shot),
`make build`, `make test`, `make vet`, `make migrate-up`.

### A note on the login contract

The JSON body is `{"email","password"}` plus a required `tenant_id`. Because
RLS is FORCED on `app.users`, you cannot look a user up *before* you know the
tenant, and the tenant cannot be discovered *from* the user without breaking
isolation — so the tenant identifier is part of the credential exchange (the
same realm pattern Auth0/Cognito use). The 401 returned on any failure is
indistinguishable whether the email, password or tenant was wrong.

### Demo credentials (slice 5)

**Live demo:** https://agro-iam.onrender.com (free tier — spins down after ~15 min
of inactivity; the first request after idle takes ~30-60s to wake it up).

`make demo` (or `go run ./cmd/seed`) seeds two tenants, each with one user per
role. Every account shares the password `test123`:

| Tenant | Admin | Agronomist | Producer | Auditor | Hauler |
|---|---|---|---|---|---|
| Coop La Esperanza | `admin@esperanza.coop` | `agronomo@esperanza.coop` | `productor@esperanza.coop` | `auditor@esperanza.coop` | `transportista@esperanza.coop` |
| Estancia El Algarrobo | `admin@algarrobo.campo` | `agronomo@algarrobo.campo` | `productor@algarrobo.campo` | `auditor@algarrobo.campo` | `transportista@algarrobo.campo` |

Password: `test123` for every account. The SPA login screen lists both realms
(tenant names) at request time — no hardcoded ids (AP2).

## Testing

Two tiers, both runnable with plain `go test` — no testcontainers, no testify:

- **Unit tests** (`internal/application/services`, `internal/infrastructure/auth`,
  `internal/http`) need **no database**. `go test ./...` runs them anywhere.
- **Integration tests** (`internal/infrastructure/postgres`,
  `internal/infrastructure/redis`) prove the RLS isolation guarantee against a
  **real PostgreSQL** and the rate limiter against **real Redis**. They skip
  themselves unless `TEST_DATABASE_URL` / `TEST_REDIS_ADDR` are set, so
  `go test ./...` stays green on a machine with no database.

### Running the integration tests locally

```bash
# 1. start postgres + redis and create the dedicated test database
make up
make test-db          # docker compose exec -T db createdb -U agroiam agroiam_test

# 2. run everything against the live DB + Redis
TEST_DATABASE_URL=postgres://agroiam:agroiam@localhost:5432/agroiam_test?sslmode=disable \
TEST_REDIS_ADDR=127.0.0.1:6379 \
  make test-integration
```

The tests own their schema: `TestMain` drops and rebuilds `app` from
`migrations/*.up.sql`, then truncates every tenanted table before each test.
That is also why CI needs nothing but a stock `postgres:16` service container —
the migrate container is for local development only.

One gotcha the harness handles for you: the official postgres Docker image makes
`POSTGRES_USER` a **superuser**, and superusers always bypass RLS — so tests that
connect as `agroiam` would "pass" without proving anything. `TestMain` detects
this and boots a dedicated non-superuser role (`agroiam_rls`, created on demand)
that owns the schema and runs every assertion, which is the same posture a
production application uses.

### Isolation guarantees

The integration tests in `internal/infrastructure/postgres` assert isolation at
the **database** level, not the repository level:

| Guarantee | Test |
|---|---|
| With no tenant context (`app.tenant_id` GUC unset), a raw `SELECT count(*) FROM app.lots` returns **zero rows** — FORCED RLS plus a NULL predicate (never true) means a missing context cannot leak | `TestTenantIsolation_RawRLS` |
| Inside a tenant-bound transaction the same table returns exactly that tenant's rows | `TestTenantIsolation_RawRLS` |
| `LotRepo.List` returns only the requesting tenant's lots; `FindByID` on another tenant's lot is `domain.ErrNotFound`, never a leak | `TestTenantIsolation_Lots` |
| `UserRepo.FindByEmail` finds a user only from that user's tenant; the same email from another tenant is `domain.ErrNotFound` | `TestTenantIsolation_Users` |
| `WITH CHECK` rejects an INSERT that claims tenant A's id while the session is bound to tenant B | `TestCrossTenantInsertBlocked` |

Together they prove the property from the architecture section: **the database
enforces isolation, not the application** — break the repository layer entirely
and a raw connection still cannot read across tenants.

## CI

`.github/workflows/ci.yml` runs `go build`, `go vet` and `go test ./...` on every
push and pull request. A `postgres:16-alpine` service container boots with the
`agroiam_test` database and the test env var set, so the RLS integration tests
execute for real in CI — no `docker-compose` file involved.

## Configuration (env)

| Variable | Default | Purpose |
|---|---|---|
| `DATABASE_URL` | — (required, fatal if unset) | pgx pool DSN |
| `REDIS_ADDR` | — (warning only if down) | go-redis address |
| `JWT_SECRET` | — (required, fatal if `change-me`) | HS256 signing key |
| `HTTP_ADDR` | `:8080` | bind address |

## Roadmap

- [x] Slice 0 — skeleton: schema + RLS, repos, auth primitives, HTTP shell
- [x] Slice 1 — JWT auth middleware, protected lot endpoints with real RLS isolation, tenant-context claims
- [x] Slice 2 — RLS isolation integration tests against live PostgreSQL (dedicated non-superuser role) + GitHub Actions CI
- [x] Slice 3 — audit event emission with hash-chaining, Redis-backed rate limiting, breach detection / token-compromise alerting
- [x] M30 — outbound breach-signal sink port (`ports.BreachSignalSink`) so severitized signals can reach future consumers
- [x] Slice 4 — campaigns & applications CRUD over the RLS repos (every query inside `WithTenant`), user provisioning (UserService + UserRoleRepository), RBAC enforcement (most-privileged role claim at token issue, `RequireRole` middleware, guarded route matrix)
- [x] Slice 5 — demo frontend / deployed demo

## License

Learning/portfolio project — no warranty.
