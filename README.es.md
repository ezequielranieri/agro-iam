# agro-iam

Plataforma multi-tenant de Gestión de Identidades y Accesos (Identity & Access Management) para el sector agrícola.
Un proyecto de aprendizaje/portfolio que demuestra **Arquitectura Clean / Hexagonal**,
tenencia por **Row Level Security (RLS)** y autenticación de defensa en profundidad (Argon2id +
JWT + rotación de familias de refresh tokens) en Go idiomático.

[![ci](https://github.com/ezequielranieri/agro-iam/actions/workflows/ci.yml/badge.svg)](https://github.com/ezequielranieri/agro-iam/actions/workflows/ci.yml)

> [English](./README.md)

> Documentación: [DECISIONS.es.md](./DECISIONS.es.md) — decisiones de arquitectura y constitución del proyecto

> Slice 0 — esqueleto base: modelo de dominio, ports, repositorios, primitivas de
> autenticación, esquema con RLS y la carcasa HTTP. Los flujos de extremo a extremo
> (aprovisionamiento de usuarios, CRUD de campañas/aplicaciones, aplicación de RBAC)
> se construyen sobre esa base y se entregan hasta el slice 4.

## El problema

Una cooperativa agrupa a cientos de productores, agrónomos, transportistas y auditores.
Los datos de cada grupo son privados: un productor de Cooperativa Río Solo jamás debe
ver los lotes, las aplicaciones o el registro de auditoría de Cooperativa Valle Norte.
Agro-iam hace que ese aislamiento sea estructural: **lo impone la base de datos**, no solo
la aplicación.

## Arquitectura

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

## Modelo de tenencia y RLS

Los tenants y los roles son **globales**: `app.tenants` es el registro de cooperativas;
`app.roles` es el catálogo compartido de roles. Ninguno lleva `tenant_id` y ninguno tiene RLS.

Toda tabla de negocio lleva `tenant_id` y está protegida por RLS **y en modo FORCED**:

```sql
CREATE POLICY tenant_isolation ON app.lots
  USING (tenant_id = app.current_tenant_id())
  WITH CHECK (tenant_id = app.current_tenant_id());
```

- `app.current_tenant_id()` lee el GUC `app.tenant_id` y devuelve `NULL` cuando no está
  definido; un predicado NULL es *siempre falso*, de modo que un contexto de tenant ausente
  produce **cero filas, nunca una fuga**.
- La aplicación fija el tenant **dentro de una transacción**:
  `SELECT set_config('app.tenant_id', $1, true)` — el `true` lo hace LOCAL.
  Un `SET` simple sobre una conexión del pool filtraría la identidad del tenant A hacia las
  conexiones reutilizadas del tenant B; el ámbito de la transacción lo impide.
- `FORCE` extiende RLS al propietario de la tabla, de modo que ni una sesión `psql` cruda ni
  una consulta con errores pueden cruzar tenants sin privilegios de superusuario.
- Las tablas con tenencia usan **claves primarias compuestas** `(id, tenant_id)`. Esto cierra
  el canal lateral por el que una clave foránea de una tabla sin proteger podría referenciar
  (y por tanto revelar) la fila de otro tenant.
- `app.refresh_tokens` está deliberadamente **sin** RLS: la búsqueda de rotación se hace por
  hash del token opaco (imposible de adivinar), y cada fila lleva el `tenant_id` necesario
  para restaurar el contexto de tenant al renovar.

## Diseño de seguridad

| Aspecto | Mecanismo |
|---|---|
| Almacenamiento de contraseñas | Argon2id (m=65536, t=1, p=4, clave de 32 bytes), codificado en PHC; `Verify` analiza el formato — nunca confía en la configuración |
| Access token | JWT HS256, claims `sub`/`tenant_id`/`role`, **TTL de 15 min**, algoritmo fijado en HS256 |
| Refresh token | Valor opaco aleatorio de 256 bits, almacenado solo como SHA-256 |
| Rotación | Cada refresh revoca el token anterior y emite un sucesor en la misma `family_id` |
| Detección de replay | Presentar un token ya rotado (revocado con `replaced_by`) revoca **toda la familia** |
| Aislamiento de tenant | PostgreSQL RLS como se describió antes |
| Integridad de auditoría | `app.audit_log` de solo apéndice con encadenamiento por hash por tenant (`seq`, `prev_hash`, `chain_hash`); `VerifyChain` detecta manipulación; los eventos de seguridad llevan severidad (info/warn/critical) |
| Limitación de tasa | Contadores de ventana fija por ruta — Lua `INCR+PEXPIRE` en Redis con fallback fail-open en memoria; `429 + Retry-After`; login 5/min/IP, refresh 30/min/IP, lots 120/min/tenant:user |
| Señales de brecha | `Detect()` dirigido por tabla clasifica los resultados en eventos con severidad emitidos a slog + auditoría; fan-out de salida `ports.BreachSignalSink` para consumidores futuros |

## Inicio rápido

Requiere: Go 1.26+, Docker con Compose.

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

Otros objetivos: `make down` (detener), `make demo` (up + seed + run, de una),
`make build`, `make test`, `make vet`, `make migrate-up`.

### Nota sobre el contrato de login

El cuerpo JSON es `{"email","password"}` más un `tenant_id` obligatorio. Como RLS está en
modo FORCED sobre `app.users`, no se puede buscar un usuario *antes* de conocer el tenant,
ni se puede descubrir el tenant *a partir* del usuario sin romper el aislamiento — por eso el
identificador de tenant forma parte del intercambio de credenciales (el mismo patrón de realm
que usan Auth0/Cognito). El 401 devuelto ante cualquier fallo es indistinguible tanto si lo
incorrecto fue el email, la contraseña o el tenant.

### Credenciales de demo (slice 5)

**Demo en vivo:** https://agro-iam.onrender.com (tier gratuito — se duerme tras
~15 min de inactividad; la primera petición tras el reposo tarda ~30-60 s en despertarlo).

`make demo` (o `go run ./cmd/seed`) siembra dos tenants, cada uno con un usuario por
rol. Todas las cuentas comparten la contraseña `test123`:

| Tenant | Admin | Agrónomo | Productor | Auditor | Transportista |
|---|---|---|---|---|---|
| Coop La Esperanza | `admin@esperanza.coop` | `agronomo@esperanza.coop` | `productor@esperanza.coop` | `auditor@esperanza.coop` | `transportista@esperanza.coop` |
| Estancia El Algarrobo | `admin@algarrobo.campo` | `agronomo@algarrobo.campo` | `productor@algarrobo.campo` | `auditor@algarrobo.campo` | `transportista@algarrobo.campo` |

Contraseña: `test123` para todas las cuentas. La pantalla de login de la SPA lista
ambos realms (nombres de tenant) en el momento de la solicitud — sin ids hardcodeados
(AP2).

## Pruebas

Dos niveles, ambos ejecutables con `go test` puro — sin testcontainers, sin testify:

- **Pruebas unitarias** (`internal/application/services`, `internal/infrastructure/auth`,
  `internal/http`) no necesitan **base de datos**. `go test ./...` las ejecuta en cualquier
  máquina.
- **Pruebas de integración** (`internal/infrastructure/postgres`,
  `internal/infrastructure/redis`) verifican la garantía de aislamiento RLS contra un
  **PostgreSQL real** y el limitador de tasa contra **Redis real**. Se omiten a sí mismas
  salvo que `TEST_DATABASE_URL` / `TEST_REDIS_ADDR` estén definidas, de modo que
  `go test ./...` sigue en verde en una máquina sin base de datos.

### Ejecutar las pruebas de integración localmente

```bash
# 1. start postgres + redis and create the dedicated test database
make up
make test-db          # docker compose exec -T db createdb -U agroiam agroiam_test

# 2. run everything against the live DB + Redis
TEST_DATABASE_URL=postgres://agroiam:agroiam@localhost:5432/agroiam_test?sslmode=disable \
TEST_REDIS_ADDR=127.0.0.1:6379 \
  make test-integration
```

Las pruebas gestionan su propio esquema: `TestMain` elimina y reconstruye `app` a partir de
`migrations/*.up.sql`, y luego trunca cada tabla con tenencia antes de cada prueba. Por eso
CI solo necesita un contenedor de servicio `postgres:16` estándar — el contenedor de migrate
es únicamente para desarrollo local.

Un detalle que el harness resuelve automáticamente: la imagen oficial de postgres en Docker crea a
`POSTGRES_USER` como **superusuario**, y los superusuarios siempre omiten RLS — por lo que
unas pruebas conectadas como `agroiam` "pasarían" sin demostrar nada. `TestMain` lo detecta
y levanta un rol dedicado sin privilegios de superusuario (`agroiam_rls`, creado bajo demanda)
que es propietario del esquema y ejecuta todas las aserciones — la misma postura que usa una
aplicación en producción.

### Garantías de aislamiento

Las pruebas de integración en `internal/infrastructure/postgres` verifican el aislamiento a
nivel de **base de datos**, no a nivel de repositorio:

| Garantía | Prueba |
|---|---|
| Sin contexto de tenant (GUC `app.tenant_id` sin definir), un `SELECT count(*) FROM app.lots` crudo devuelve **cero filas** — RLS FORCED más un predicado NULL (nunca verdadero) impide que un contexto ausente filtre datos | `TestTenantIsolation_RawRLS` |
| Dentro de una transacción ligada a un tenant, la misma tabla devuelve exactamente las filas de ese tenant | `TestTenantIsolation_RawRLS` |
| `LotRepo.List` devuelve solo los lotes del tenant solicitante; `FindByID` sobre un lote de otro tenant devuelve `domain.ErrNotFound`, nunca una fuga | `TestTenantIsolation_Lots` |
| `UserRepo.FindByEmail` encuentra a un usuario solo dentro del tenant de ese usuario; el mismo email en otro tenant devuelve `domain.ErrNotFound` | `TestTenantIsolation_Users` |
| `WITH CHECK` rechaza un INSERT que reclame el id del tenant A mientras la sesión está ligada al tenant B | `TestCrossTenantInsertBlocked` |

En conjunto demuestran la propiedad de la sección de arquitectura: **la base de datos impone
el aislamiento, no la aplicación** — rompe por completo la capa de repositorios y aun así una
conexión cruda no puede leer entre tenants.

## CI

`.github/workflows/ci.yml` ejecuta `go build`, `go vet` y `go test ./...` en cada push y
pull request. Un contenedor de servicio `postgres:16-alpine` arranca con la base de datos
`agroiam_test` y la variable de entorno de prueba definida, de modo que las pruebas de
integración de RLS se ejecutan de verdad en CI — sin archivo `docker-compose` de por medio.

## Configuración (env)

| Variable | Default | Propósito |
|---|---|---|
| `DATABASE_URL` | — (obligatoria, fatal si no está definida) | DSN del pool pgx |
| `REDIS_ADDR` | — (solo advertencia si está caído) | dirección de go-redis |
| `JWT_SECRET` | — (obligatoria, fatal si es `change-me`) | clave de firma HS256 |
| `HTTP_ADDR` | `:8080` | dirección de bind |

## Hoja de ruta

- [x] Slice 0 — esqueleto: esquema + RLS, repos, primitivas de auth, carcasa HTTP
- [x] Slice 1 — middleware de auth JWT, endpoints de lotes protegidos con aislamiento RLS real, claims de contexto de tenant
- [x] Slice 2 — pruebas de integración del aislamiento RLS contra PostgreSQL real (rol dedicado sin superusuario) + CI en GitHub Actions
- [x] Slice 3 — emisión de eventos de auditoría con encadenamiento por hash, limitación de tasa respaldada por Redis, detección de brechas / alertas de compromiso de tokens
- [x] M30 — port de salida de señales de brecha (`ports.BreachSignalSink`) para que las señales con severidad puedan llegar a consumidores futuros
- [x] Slice 4 — CRUD de campañas y aplicaciones sobre los repos RLS (cada consulta dentro de `WithTenant`), aprovisionamiento de usuarios (UserService + UserRoleRepository), aplicación de RBAC (claim del rol más privilegiado al emitir el token, middleware `RequireRole`, matriz de rutas protegidas)
- [x] Slice 5 — frontend de demostración / demo desplegada

## Licencia

Proyecto de aprendizaje/portfolio — sin garantía.
