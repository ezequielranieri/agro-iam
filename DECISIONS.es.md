# DECISIONS.es.md — Constitución del proyecto `agro-iam`

🇬🇧 [English version](./DECISIONS.md)

Este archivo es la fuente de verdad para cualquier agente (humano o IA) que
trabaje en este repositorio. Toda implementación debe respetar estas reglas. Si
una tarea entra en conflicto con algo definido aquí, **detente y consulta antes
de proceder**.

Cada decisión registra: la regla (lo que el código DEBE hacer), las alternativas
descartadas y por qué, y el coste que el proyecto acepta por elegirla. Esta
última parte importa: conocer el coste de una decisión es lo que permite
reabrirla más adelante con una justificación sólida en lugar de una corazonada.

Leyenda de estados: `Accepted` = establecido; `Open` = propuesta, no
implementar.

---

## 1. Pila tecnológica (fija, no negociable sin discusión explícita)

- **Lenguaje:** Go 1.26+ (`go 1.26` en `go.mod`; CI fija `1.26.x`).
- **Persistencia:** PostgreSQL 16 (`postgres:16-alpine`). Es la fuente de
  verdad, y **Row Level Security (RLS) es el mecanismo de tenencia** — la base
  de datos impone el aislamiento, no la aplicación.
- **Redis:** Redis 7 (`redis:7-alpine`). Se usa **solo para la factoría de
  clientes** (`internal/infrastructure/redis`) hoy — **NO es requisito para los
  flujos principales**: el arranque registra una advertencia y continúa sirviendo
  si Redis está caído. Es un placeholder de conexión, no una dependencia de
  caché; ningún flujo principal lee de Redis todavía. En el Slice 3 (limitación
  de tasa, detección de brechas) es donde Redis empezará a aportar.
- **Acceso a base de datos:** `pgx/v5` directo — `pgxpool` para conexiones,
  `pgx.Tx` para transacciones ligadas a tenant. Sin ORM.
- **HTTP:** `net/http` estándar `http.ServeMux` con enrutado por patrones de Go
  1.22+ (`GET /api/v1/lots`, parámetros de ruta `{id}`). Sin chi/gin/echo.
- **Logging:** `log/slog` estándar. Sin zerolog/zap/logrus.
- **Primitivas de autenticación** (`internal/infrastructure/auth`): JWT HS256
  (`golang-jwt/jwt/v5`), Argon2id (`golang.org/x/crypto/argon2`), familias de
  refresh tokens opacos con SHA-256.
- **Migraciones:** `golang-migrate`, SQL versionado en `migrations/`
  (contenedor de migrate en Docker Compose).
- **Configuración:** `os.Getenv` directo en `cmd/api/main.go` — deliberadamente
  sin librería externa de configuración.
- **Pruebas:** `go test` estándar — pruebas unitarias (sin base de datos) más
  pruebas de integración contra PostgreSQL real que se omiten a sí mismas salvo
  que `TEST_DATABASE_URL` esté definida; CI las ejecuta contra un contenedor de
  servicio `postgres:16-alpine`.
- **Infraestructura local:** Docker Compose (`postgres:16-alpine` +
  `redis:7-alpine` + contenedor de migrate).

**Por qué estas elecciones (y qué se descartó)**
- **RLS frente a esquema-por-tenant / base-de-datos-por-tenant:** el producto
  apunta a *muchos tenants pequeños* compartiendo una única instancia. RLS da un
  aislamiento estructural que sobrevive a SQL crudo y a consultas con errores,
  con un solo esquema, una sola cadena de migraciones y backups simples
  (ver 2.1).
- **`pgx/v5` frente a un ORM:** RLS depende del control preciso de
  `set_config`, de los límites de transacción, de las claves compuestas y de las
  políticas raw — exactamente las características que los ORM ocultan o
  combaten (ver 2.10).
- **`ServeMux` + `slog` estándar frente a chi/gin/echo + zerolog/zap/logrus:**
  cero dependencias de terceros para enrutado y logging; el comportamiento se
  ajusta a las garantías de la biblioteca estándar; el stack de middleware se
  mantiene pequeño y revisable (ver 2.11).
- **Argon2id frente a bcrypt:** bcrypt resiste mal los ataques ASIC/GPU porque
  su huella de memoria es mínima; Argon2id es memory-hard (ver 2.8).
- **HS256 frente a RS256 por ahora:** simplicidad simétrica con un único secreto
  compartido; RS256/JWKS es el camino documentado cuando se necesite verificación
  multi-servicio (ver 2.6).
- **Monolito modular frente a microservicios:** el dominio es pequeño; los
  microservicios fragmentarían el modelo de tenencia, el manejo de sesiones RLS y
  la rotación de refresh tokens entre fronteras de proceso (ver 2.12).
- **Redis es un placeholder, no una dependencia:** hoy solo existe la factoría
  de clientes; se conecta ahora para que `cmd/api` no cambie después, pero ningún
  flujo principal depende de él — una diferencia deliberada frente a una
  dependencia de caché.

---

## 2. Decisiones de diseño confirmadas (no reabrir sin justificación sólida)

> Estado de cada subsección: Accepted — no reabrir sin justificación sólida (ver preámbulo).

### 2.1 RLS como mecanismo de aplicación de la tenencia — Estado: Accepted

**Regla**
- La tenencia DEBE imponerse mediante **PostgreSQL Row Level Security**, nunca
  solo por la capa de aplicación. El aislamiento DEBE resistir una consulta con
  errores, una capa de repositorios rota o una sesión `psql` cruda.
- Toda tabla con tenencia DEBE llevar `tenant_id`, DEBE tener RLS habilitado y
  DEBE tener una política que filtre las filas por `tenant_id =
  app.current_tenant_id()`.
- `app.tenants` (registro de cooperativas) y `app.roles` (catálogo de roles
  compartido) son **globales**: ninguno lleva `tenant_id`, ninguno tiene RLS y
  ninguno puede recibir tenencia.
- La aplicación DEBE fijar el contexto de tenant por transacción (ver 2.4) y
  dejar que la base de datos decida qué filas son visibles.

**Alternativas descartadas**
- *Esquema-por-tenant*: cientos de tenants pequeños harían explotar el catálogo
  y las herramientas de migración/backup; el producto necesita operaciones
  baratas por tenant.
- *Base-de-datos-por-tenant*: la infraestructura dedicada por tenant debe ser
  barata de operar; una sola instancia sirve a miles de tenants pequeños.
- *Filtrado solo en la capa de aplicación* (`WHERE tenant_id = ...`): no es
  estructural — una consulta con errores o una sesión SQL cruda lo omiten; el
  aislamiento debe sobrevivir a bugs de aplicación.

**Coste aceptado**
- Toda consulta necesita contexto de tenant o devuelve silenciosamente cero
  filas — un GUC ausente produce un predicado NULL siempre falso, por lo que
  depurar un resultado "vacío" exige conocer el GUC.
- Mecanismo solo-PostgreSQL: RLS no tiene equivalente portable, así que la
  elección de base de datos queda fijada.
- Una conexión sin contexto puede confundir a quienes esperan una vista global.

### 2.2 FORCE RLS en todas las tablas con tenencia — Estado: Accepted

**Regla**
- Toda tabla con tenencia DEBE tener `ALTER TABLE ... FORCE ROW LEVEL SECURITY`
  además de `ENABLE`. FORCE extiende RLS al propietario de la tabla, de modo que
  cualquier rol normal queda restringido; solo los superusuarios omiten RLS, por
  diseño.
- Aplica a: `app.users`, `app.user_roles`, `app.lots`, `app.campaigns`,
  `app.applications`, `app.audit_log`.

**Alternativas descartadas**
- *ENABLE RLS sin FORCE*: el propietario de la tabla — a menudo el usuario de
  migraciones o un DBA — omite RLS por completo, de modo que una consulta
  accidentalmente sin filtro desde la conexión de la aplicación podría cruzar
  tenants.
- *Confiar en que la aplicación siempre filtre por tenant*: el propósito de
  FORCE es la defensa en profundidad contra una sesión `psql` cruda o un error de
  un DBA, no solo frente a una capa de aplicación bien comportada.

**Coste aceptado**
- Las operaciones de migración y seed que deben tocar todos los tenants (p. ej.
  backfills globales) requieren privilegios elevados.
- El harness de pruebas de integración necesita un rol dedicado sin privilegios
  de superusuario (`agroiam_rls`), porque una conexión de superusuario "aprueba"
  las aserciones RLS de forma vacua — los superusuarios siempre omiten RLS.

### 2.3 Claves primarias compuestas (id, tenant_id) — Estado: Accepted

**Regla**
- Las tablas de negocio con tenencia DEBEN usar una **clave primaria compuesta
  `(id, tenant_id)`** — como en `app.lots`, `app.campaigns`,
  `app.applications`.
- Una clave foránea o un `JOIN` que referencie una fila con tenencia DEBE incluir
  el tenant: una clave referenciadora no puede apuntar a la fila de otro tenant
  sin nombrar también a ese tenant.

**Alternativas descartadas**
- *Clave primaria simple `id`*: crea un canal lateral — una clave foránea en una
  tabla sin protección, o cualquier `JOIN`, puede referenciar la fila de otro
  tenant por su `id` simple, y una consulta que devuelva ese id revela que la fila
  existe aunque RLS oculte sus columnas.

**Coste aceptado**
- Las referencias `FOREIGN KEY` a tablas con tenencia deben repetir el par de
  columnas compuesto.
- Los ORM y las herramientas que asumen clave primaria de una sola columna
  necesitan adaptación — agro-iam evita los ORM (ver 2.10).

### 2.4 Contexto de tenant fijado vía set_config por transacción — Estado: Accepted

**Regla**
- El contexto de tenant DEBE fijarse con `SELECT set_config('app.tenant_id',
  $1, true)` dentro de una **transacción dedicada** — el `true` hace el ajuste
  LOCAL a esa transacción. Nunca un `SET` simple sobre una conexión del pool.
- Toda unidad de trabajo con alcance de tenant DEBE ejecutarse a través del
  helper `WithTenant` en `internal/infrastructure/postgres/db.go`, que inicia la
  transacción, fija el GUC, ejecuta la unidad de trabajo contra la transacción y
  hace commit (o rollback) como un único límite atómico.

**Alternativas descartadas**
- *`SET` simple sobre una conexión del pool*: el ajuste persiste en esa conexión
  al terminar la unidad de trabajo; el pool la reutiliza para las consultas del
  tenant B mientras todavía lleva la identidad del tenant A — filtrando el
  contexto del tenant A hacia las peticiones del tenant B.
- *Fijar el GUC fuera de una transacción (scripts ad-hoc)*: sin límite atómico, y
  es el clásico foot-gun para futuros contribuidores.

**Coste aceptado**
- Toda unidad de trabajo con alcance de tenant paga el coste de una transacción.
- Fijar el GUC fuera del helper `WithTenant` es el principal foot-gun para
  futuros contribuidores; una unidad de trabajo completa observa un único
  contexto de tenant (sin lecturas cross-tenant dentro de una transacción).

### 2.5 El contrato de login incluye tenant_id (patrón de realm) — Estado: Accepted

**Regla**
- El contrato de login DEBE ser `{email, password, tenant_id}`: la aplicación
  fija el tenant PRIMERO y solo después resuelve el usuario. Es el mismo patrón
  de realm que usan Auth0/Cognito, donde el tenant (realm) forma parte del
  intercambio de credenciales.
- Cualquier fallo DEBE devolver un **401** uniforme, indistinguible de si lo
  incorrecto fue el email, la contraseña o el tenant.
- Razón: RLS está en modo FORCED sobre `app.users` (ver 2.2), de modo que una
  fila de usuario es invisible salvo que la sesión esté ligada al tenant de ese
  usuario — un login que busca al usuario por email primero devuelve nada antes
  de conocer el tenant, y el tenant no puede derivarse del usuario sin romper el
  aislamiento.

**Alternativas descartadas**
- *Buscar al usuario por email y después comprobar el tenant*: la búsqueda no
  devuelve nada antes de conocer el tenant; el tenant no puede derivarse del
  usuario sin romper el aislamiento.
- *Respuestas de error diferenciadas*: revelarían qué componente de las
  credenciales (email, contraseña, tenant) falló.

**Coste aceptado**
- El cliente debe conocer el identificador de su tenant de antemano; un
  `tenant_id` perdido no puede recuperarse a través de la API.
- La superficie de la API difiere de las convenciones de login de un solo tenant.
- Un 401 uniforme impide que el cliente sepa qué campo corregir solo a partir de
  la respuesta.

### 2.6 JWTs HS256 de corta duración con algoritmo fijado — Estado: Accepted

**Regla**
- Los access tokens DEBEN ser **JWTs HS256** con un **TTL de 15 minutos** por
  defecto (`accessTokenTTL` en `cmd/api/main.go`), con los claims `sub` (id de
  usuario), `tenant_id` y `role`.
- El algoritmo DEBE estar **fijado a HS256**: la verificación DEBE rechazar
  cualquier otro método de firma, cerrando los ataques clásicos de `alg=none` y
  de confusión de claves.

**Alternativas descartadas**
- *Tokens de sesión opacos (estado en servidor)*: sin problema de revocación,
  pero exigen estado en el servidor por petición; se eligieron tokens stateless
  por simplicidad.
- *RS256 (asimétrico)*: más piezas de gestión de claves; descartado por ahora en
  favor de la simplicidad de implementación, con un camino documentado a
  RS256/JWKS cuando se necesite verificación multi-servicio.

**Coste aceptado**
- HS256 es simétrico — el secreto firma y verifica a la vez; un `JWT_SECRET`
  filtrado forja tokens para cualquier tenant, por lo que el secreto debe rotarse
  periódicamente.
- Los access tokens no pueden revocarse antes de su expiración (mitigado por el
  TTL corto).
- Un segundo servicio solo puede verificar los tokens con los que comparte el
  secreto.

### 2.7 Refresh tokens opacos almacenados como SHA-256 con rotación por familias — Estado: Accepted

**Regla**
- Los refresh tokens DEBEN ser **valores opacos aleatorios de 256 bits**
  (`crypto/rand`, base64url) devueltos al cliente exactamente una vez y
  persistidos **solo como su digest SHA-256**
  (`internal/application/services/refresh.go`). TTL por defecto: 30 días
  (`refreshTokenTTL` en `cmd/api/main.go`).
- La rotación DEBE mantener una **cadena por familia**: cada refresh revoca el
  token anterior, emite un sucesor en la misma `family_id` y registra
  `replaced_by`.
- Presentar un token ya rotado (revocado con `replaced_by` definido) ES la firma
  de replay y DEBE revocar **toda la familia** (`RevokeFamily`), matando también
  al sucesor recién emitido del atacante.

**Alternativas descartadas**
- *Almacenar los refresh tokens en claro*: una brecha en la base de datos
  permitiría al atacante reproducir el token indefinidamente.
- *Rotación sin seguimiento de familia*: en la carrera clásica donde el atacante
  y el usuario legítimo presentan el mismo token, quien llegue segundo no debe
  tener éxito — sin revocación de toda la familia, el sucesor robado del
  atacante sobrevive.

**Coste aceptado**
- La rotación añade estado y escrituras sensibles a la concurrencia.
- Un sucesor perdido o no transmitido tras un refresh obliga a un nuevo login.
- El replay de un token *expirado* no debe clasificarse como robo — la lógica de
  rotación debe distinguir Expired de Revoked.

### 2.8 Hashing de contraseñas con Argon2id en lugar de bcrypt — Estado: Accepted

**Regla**
- Las contraseñas DEBEN cifrarse con **Argon2id** usando los parámetros OWASP
  para logins interactivos `m=65536, t=1, p=4` (64 MiB de memoria, 1 iteración,
  4 lanes), una clave de 32 bytes y un salt aleatorio de 16 bytes.
- Los hashes DEBEN codificarse en el **formato PHC**
  (`$argon2id$v=19$m=65536,t=1,p=4$...`), y `Verify` DEBE re-derivar los
  parámetros de la propia cadena almacenada — nunca de la configuración — de modo
  que una futura subida de parámetros sea transparente para los hashes
  existentes.

**Alternativas descartadas**
- *bcrypt*: su huella de memoria es mínima, por lo que resiste mal la fuerza
  bruta ASIC/GPU por diseño; la recomendación de la comunidad para logins
  interactivos ha avanzado.

**Coste aceptado**
- Cifrar cuesta 64 MiB por llamada — más CPU/memoria por login.
- No es una primitiva aprobada por FIPS en algunos contextos de cumplimiento.
- Los parámetros están fijos en código, por lo que re-hashear ante un cambio de
  parámetros es una migración.

### 2.9 app.refresh_tokens deliberadamente sin protección RLS — Estado: Accepted

**Regla**
- `app.refresh_tokens` DEBE **no** estar protegida por RLS. La búsqueda se hace
  por el digest del token (imposible de adivinar, ver 2.7), y cada fila DEBE
  llevar su propio `tenant_id` para que el flujo pueda restaurar el contexto de
  tenant tras una rotación exitosa.
- El modelo de amenaza cambia: el token *es* la capacidad, y la fuerza
  criptográfica del hash es lo que protege la tabla.

**Alternativas descartadas**
- *Proteger `app.refresh_tokens` con RLS*: en el momento del refresh el access
  token suele estar expirado, de modo que el cliente no puede aportar un tenant
  que la aplicación pueda confiar como propio — la búsqueda de rotación sería
  imposible sin reintroducir el mismo canal lateral que el diseño evita.

**Coste aceptado**
- La tabla es legible por cualquier rol con SELECT a nivel de tabla — su
  seguridad descansa por completo en la imposibilidad de adivinar los hashes
  almacenados.
- Un volcado completo de la tabla es sensible aunque no pueda reproducirse.

### 2.10 pgx/v5 directo sin ORM — Estado: Accepted

**Regla**
- Todo el acceso a PostgreSQL DEBE usar **`pgx/v5` directamente** — `pgxpool`
  para conexiones, `pgx.Tx` para transacciones ligadas a tenant — con SQL escrito
  a mano para cada consulta. Sin ORM (sin GORM, sin la ruta de código generado de
  sqlc, sin ent).

**Alternativas descartadas**
- *ORM (GORM, sqlc codegen, ent)*: los ORM suelen asumir claves de una sola
  columna, gestionan sus propios ajustes de conexión y generan SQL que omite el
  contexto de tenant — ocultan o combaten `set_config`, los límites de
  `WithTenant` y las claves primarias compuestas (ver 2.3, 2.4).

**Coste aceptado**
- Toda consulta se escribe y se revisa a mano; sin mapeo automático, el escaneo
  de structs es explícito.
- El esquema (migraciones) y las consultas deben mantenerse sincronizados
  manualmente.

### 2.11 http.ServeMux y slog estándar — Estado: Accepted

**Regla**
- El enrutado HTTP DEBE usar el **`net/http` `http.ServeMux` estándar** con
  enrutado por patrones de Go 1.22+ (`GET /api/v1/lots`, parámetros de ruta
  `{id}`) y el logger estructurado **`log/slog` estándar**.
- El middleware (logging, recovery, request-id, `RequireAuth`) DEBE consistir en
  wrappers de `http.Handler` simples. chi/gin/echo y zerolog/zap/logrus no se
  usan.

**Alternativas descartadas**
- *chi / gin / echo*: routers de terceros con sus propias convenciones de
  middleware, parámetros de ruta y handlers — dependencias sin ningún hueco que
  llenar.
- *zerolog / zap / logrus*: loggers de terceros; `slog` cubre el logging
  estructurado.

**Coste aceptado**
- El enrutado estándar tiene un conjunto de características menor que chi/gin
  (sin cadenas de middleware, sin grupos de middleware por ruta — los handlers
  componen explícitamente).
- Sin características de logging estructurado más allá del núcleo de slog.
- El enrutado por patrones requiere Go 1.22+.

### 2.12 Monolito modular con capas clean/hexagonales — Estado: Accepted

**Regla**
- El sistema DEBE ser un **monolito modular** con capas clean/hexagonales:
  - `internal/domain` — entidades puras, cero dependencias externas (solo
    estándar);
  - `internal/application` — casos de uso que dependen solo de las interfaces
    `ports` (DIP);
  - `internal/infrastructure` — adaptadores: `postgres`, `redis`, `auth`;
  - `internal/http` — transporte estándar;
  - compuesto en `cmd/api` (composition root).
- Los microservicios están descartados.

**Alternativas descartadas**
- *Microservicios*: fragmentarían el modelo de tenencia, el manejo de sesiones
  RLS y la lógica de rotación de refresh tokens entre fronteras de proceso, e
  introducirían modos de fallo de red, tooling de despliegue y pruebas
  distribuidas antes de que exista una necesidad.

**Coste aceptado**
- Un monolito escala por escalado vertical o replicación, no por despliegue
  independiente de servicios.
- Un solo proceso concentra toda la superficie de seguridad — un compromiso lo
  afecta todo.
- Se requiere disciplina para mantener las capas honestas conforme crece el
  código.

### 2.13 Hash-chaining de auditoría por tenant, a prueba de manipulación — Estado: Accepted

**Regla**
- El trail de auditoría DEBE estar **encadenado por hash por tenant**: cada fila
  de `app.audit_log` lleva `seq`, `prev_hash` y `chain_hash` con
  `UNIQUE (tenant_id, seq)`.
- La entrada génesis usa `seq = 1` y `prev_hash` = 64 ceros hex; el `prev_hash`
  de cada fila siguiente DEBE ser igual al `chain_hash` de la fila anterior.
- `chain_hash` DEBE ser `SHA-256` sobre el orden de campos fijo
  `prev_hash|seq|tenant_id|actor_user_id|action|entity_type|entity_id|payload|created_at`
  donde `payload` es el JSON **canonicalizado** (decode → re-marshal) y
  `created_at` está truncado a microsegundos (resolución de `timestamptz` de
  Postgres) — el mismo code path exacto para insertar y verificar, sin drift.
- La verificación de cadena (`VerifyChain`) DEBE ser interna: recalcular la
  cadena desde todas las filas almacenadas y reportar el primer `seq` roto. Sin
  endpoint público (alcance del slice 3).
- Los appends de auditoría DEBEN ejecutarse dentro de `WithTenant` (de lo
  contrario el RLS `FORCE` rechaza el insert) y DEBEN ser fail-open: un fallo de
  auditoría nunca tumba el flujo principal, solo un WARN.
- La PK de una sola columna `id` de `app.audit_log` es una desviación deliberada
  de la regla 2.3: la tabla nunca es referenciada por una clave foránea, así que
  el canal lateral de la PK compuesta no aplica.

**Alternativas descartadas**
- *Cadena global (una secuencia para todos los tenants)*: una sesión de tenant no
  puede leer filas de otros tenants bajo RLS, así que el append se rompería;
  además serializa a todos los tenants en una fila caliente y filtra el orden
  cross-tenant.
- *Trigger en la base que calcula el hash*: cero drift, pero lógica de seguridad
  escondida en SQL de migración, no testeable en Go puro, y en contra de la
  convención de mantener la lógica en los servicios.
- *Hashear la fila SQL completa*: cualquier migración futura que agregue una
  columna invalidaría toda la cadena.

**Coste aceptado**
- Una lectura indexada de cola + un insert por evento de auditoría (sync, en el
  camino de escritura).
- La verificación es O(n) — aceptable a volumen de portfolio, revisar si crece.
- Los payloads deben mantenerse float64-safe para que la canonicalización sea
  estable; el test de integración lo fija.

---

## 3. Convenciones de código

- **El dominio es puro**: `internal/domain` no importa nada más que la biblioteca
  estándar — entidades y errores centinela, cero dependencias externas.
- **El SQL vive en dos lugares**: `migrations/` y
  `internal/infrastructure/postgres`. El SQL nunca se llama directamente desde
  los handlers de `internal/http` ni desde los servicios de
  `internal/application`.
- **Todo acceso con alcance de tenant pasa por el helper `WithTenant`**
  (`internal/infrastructure/postgres/db.go`); los repositorios ejecutan cada
  consulta dentro de él para que RLS sea el punto de aplicación, no una cláusula
  `WHERE`.
- **Los errores se mapean en la frontera del adaptador**: los errores del driver
  pgx se traducen a errores de dominio (`mapPgErr` — p. ej. `unique_violation`
  23505 → `domain.ErrConflict`); el código de aplicación nunca depende de los
  tipos de error de pgx.
- **Conciencia de claves compuestas**: las tablas con tenencia se identifican por
  `(id, tenant_id)`; las claves foráneas repiten el par compuesto.
- **Nombres**: tablas y columnas en `snake_case`; tipos y funciones de Go en
  estilo idiomático (`CamelCase` / `camelCase`).
- **Los comentarios se escriben en inglés** (EN).
- **Configuración**: `os.Getenv` directo — sin librería externa de
  configuración.

---

## 4. Prohibiciones explícitas

- No desactivar, debilitar ni omitir FORCE RLS en ninguna tabla con tenencia; no
  ejecutar consultas con alcance de tenant fuera del helper `WithTenant`.
- No añadir un ORM (GORM, sqlc codegen, ent o similar) — solo SQL pgx escrito a
  mano.
- No añadir chi, gin, echo ni zerolog/zap/logrus — solo `ServeMux` y `slog`
  estándar.
- No almacenar refresh tokens ni contraseñas en claro — solo digests SHA-256 y
  cadenas PHC de Argon2id.
- No permitir flexibilidad de algoritmo JWT — HS256 está fijado; la verificación
  rechaza cualquier otro método (sin `alg=none`, sin confusión de claves).
- No añadir `tenant_id` a las tablas globales `app.tenants` y `app.roles` — son
  globales por diseño.
- No añadir RLS a `app.refresh_tokens` — está deliberadamente sin proteger
  (ver 2.9).
- No introducir esquema-por-tenant ni base-de-datos-por-tenant — RLS sobre una
  única instancia compartida es el mecanismo de tenencia (ver 2.1).

---

## 5. Hoja de ruta de referencia (alto nivel)

Del README (slices 0–5 tal como figuran actualmente):

- [x] **Slice 0** — esqueleto: esquema + RLS, repos, primitivas de auth, carcasa
  HTTP
- [x] **Slice 1** — middleware de auth JWT, endpoints de lotes protegidos con
  aislamiento RLS real, claims de contexto de tenant
- [x] **Slice 2** — pruebas de integración del aislamiento RLS contra PostgreSQL
  real (rol dedicado sin superusuario) + CI en GitHub Actions
- [ ] **Slice 3** — emisión de eventos de auditoría con encadenamiento por hash,
  limitación de tasa respaldada por Redis, detección de brechas / alertas de
  compromiso de tokens
- [ ] **Slice 4** — CRUD de campañas y aplicaciones sobre los repos RLS,
  aprovisionamiento de usuarios, aplicación de RBAC
- [ ] **Slice 5** — frontend de demostración / demo desplegada

---

## Cómo añadir una decisión

1. Elige el siguiente número libre (`### 2.13`, ...) en la sección 2.
2. Copia la plantilla siguiente, complétala y mantén la sección ordenada.

```markdown
### 2.N <Título> — Estado: Accepted

**Regla**
- <lo que el código DEBE hacer, como bullets en modo MUST>

**Alternativas descartadas**
- *<alternativa>*: <razón técnica por la que se descartó>

**Coste aceptado**
- <compromiso honesto que el proyecto acepta>
```

3. Actualiza el espejo en español en `DECISIONS.es.md` en la misma sesión para
   que ambos idiomas se mantengan sincronizados.
