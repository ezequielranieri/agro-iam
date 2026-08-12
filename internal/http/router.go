// Package http wires the stdlib HTTP server: a Go 1.22+ ServeMux, slog
// middleware and the handlers.
package http

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/ezequielranieri/agro-iam/internal/application/ports"
	"github.com/ezequielranieri/agro-iam/internal/application/services"
	"github.com/ezequielranieri/agro-iam/internal/domain"
	"github.com/ezequielranieri/agro-iam/internal/http/claims"
	"github.com/ezequielranieri/agro-iam/internal/http/handlers"
	"github.com/ezequielranieri/agro-iam/internal/infrastructure/redis"
	"github.com/ezequielranieri/agro-iam/internal/requestid"
)

// Server bundles every dependency the HTTP layer needs.
type Server struct {
	auth         ports.AuthService
	tokens       ports.TokenManager
	lots         ports.LotService
	campaigns    ports.CampaignService
	applications ports.ApplicationService
	users        ports.UserService
	audit        ports.AuditService
	tenants      ports.TenantRepository
	rateLimiter  *redis.RateLimiter
	signals      ports.BreachSignalSink
	log          *slog.Logger
}

// NewServer builds the server with its dependencies.
// rateLimiter may be nil (rate limiting disabled); signals may be nil (breach
// emission is a no-op).
func NewServer(auth ports.AuthService, tokens ports.TokenManager, lots ports.LotService, campaigns ports.CampaignService, applications ports.ApplicationService, users ports.UserService, audit ports.AuditService, tenants ports.TenantRepository, rateLimiter *redis.RateLimiter, signals ports.BreachSignalSink, log *slog.Logger) *Server {
	return &Server{auth: auth, tokens: tokens, lots: lots, campaigns: campaigns, applications: applications, users: users, audit: audit, tenants: tenants, rateLimiter: rateLimiter, signals: signals, log: log}
}

// Routes assembles the stdlib ServeMux. Path parameters use the Go 1.22
// `{param}` syntax â€” no third-party router is needed. Health and auth routes
// are public; lot routes are wrapped with s.requireAuth so the authenticated
// tenant is always present in the request context before a handler runs.
// Rate limiting is applied per-route: login 5/min/IP, refresh 30/min/IP,
// lots 120/min/tenant:user (after auth).
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	authHandler := handlers.NewAuthHandler(s.auth, s.log)

	// Healthz: no rate limit (exempt in middleware)
	mux.HandleFunc("GET /healthz", handlers.Health)

	tenantsHandler := handlers.NewTenantsHandler(s.tenants, s.log)

	// Tenant registry: public, credentials-free (AP2). The realm list must be
	// readable before login so the demo screen can render the tenant selector;
	// it is deliberately NOT gated by auth, role or rate limit.
	mux.Handle("GET /api/v1/tenants", http.HandlerFunc(tenantsHandler.List))

	// Auth routes: rate limit per IP, BEFORE auth (public routes)
	mux.Handle("POST /api/v1/auth/login",
		s.rateLimit(5, time.Minute)(http.HandlerFunc(authHandler.Login)))
	mux.Handle("POST /api/v1/auth/refresh",
		s.rateLimit(30, time.Minute)(http.HandlerFunc(authHandler.Refresh)))

	lotsHandler := handlers.NewLotsHandler(s.lots, s.log)

	// API routes: auth FIRST, then rate limit per tenant:user
	mux.Handle("GET /api/v1/lots",
		s.requireAuth(s.rateLimit(120, time.Minute)(http.HandlerFunc(lotsHandler.List))))
	mux.Handle("POST /api/v1/lots",
		s.requireAuth(s.rateLimit(120, time.Minute)(http.HandlerFunc(lotsHandler.Create))))

	campaignsHandler := handlers.NewCampaignsHandler(s.campaigns, s.log)

	// Campaign routes: reads for any authenticated user; writes are gated by
	// the RequireRole matrix (admin | agronomist, R4/R15). Composition is
	// auth -> role -> rate limit, so a forbidden role never burns rate-limit
	// quota (D6).
	mux.Handle("GET /api/v1/campaigns",
		s.requireAuth(s.rateLimit(120, time.Minute)(http.HandlerFunc(campaignsHandler.List))))
	mux.Handle("POST /api/v1/campaigns",
		s.requireAuth(s.requireRole(domain.RoleAdmin, domain.RoleAgronomist)(s.rateLimit(120, time.Minute)(http.HandlerFunc(campaignsHandler.Create)))))
	mux.Handle("GET /api/v1/campaigns/{id}",
		s.requireAuth(s.rateLimit(120, time.Minute)(http.HandlerFunc(campaignsHandler.GetByID))))
	mux.Handle("PATCH /api/v1/campaigns/{id}",
		s.requireAuth(s.requireRole(domain.RoleAdmin, domain.RoleAgronomist)(s.rateLimit(120, time.Minute)(http.HandlerFunc(campaignsHandler.Update)))))
	mux.Handle("DELETE /api/v1/campaigns/{id}",
		s.requireAuth(s.requireRole(domain.RoleAdmin, domain.RoleAgronomist)(s.rateLimit(120, time.Minute)(http.HandlerFunc(campaignsHandler.Delete)))))

	applicationsHandler := handlers.NewApplicationsHandler(s.applications, s.log)

	// Application routes: reads for any authenticated user; writes are gated by
	// the RequireRole matrix (admin | agronomist | producer, R8/R15).
	mux.Handle("GET /api/v1/applications",
		s.requireAuth(s.rateLimit(120, time.Minute)(http.HandlerFunc(applicationsHandler.List))))
	mux.Handle("POST /api/v1/applications",
		s.requireAuth(s.requireRole(domain.RoleAdmin, domain.RoleAgronomist, domain.RoleProducer)(s.rateLimit(120, time.Minute)(http.HandlerFunc(applicationsHandler.Create)))))
	mux.Handle("GET /api/v1/applications/{id}",
		s.requireAuth(s.rateLimit(120, time.Minute)(http.HandlerFunc(applicationsHandler.GetByID))))
	mux.Handle("PATCH /api/v1/applications/{id}",
		s.requireAuth(s.requireRole(domain.RoleAdmin, domain.RoleAgronomist, domain.RoleProducer)(s.rateLimit(120, time.Minute)(http.HandlerFunc(applicationsHandler.Update)))))
	mux.Handle("DELETE /api/v1/applications/{id}",
		s.requireAuth(s.requireRole(domain.RoleAdmin, domain.RoleAgronomist, domain.RoleProducer)(s.rateLimit(120, time.Minute)(http.HandlerFunc(applicationsHandler.Delete)))))

	usersHandler := handlers.NewUsersHandler(s.users, s.log)

	// Provisioning routes: admin only (R12/R15). Every user route is gated —
	// reads included — because the user directory is tenant-administrative data.
	mux.Handle("POST /api/v1/users",
		s.requireAuth(s.requireRole(domain.RoleAdmin)(s.rateLimit(120, time.Minute)(http.HandlerFunc(usersHandler.Create)))))
	mux.Handle("GET /api/v1/users",
		s.requireAuth(s.requireRole(domain.RoleAdmin)(s.rateLimit(120, time.Minute)(http.HandlerFunc(usersHandler.List)))))
	mux.Handle("PATCH /api/v1/users/{id}",
		s.requireAuth(s.requireRole(domain.RoleAdmin)(s.rateLimit(120, time.Minute)(http.HandlerFunc(usersHandler.Update)))))

	auditHandler := handlers.NewAuditHandler(s.audit, s.log)

	// Audit trail read: admin only (AP1). Composition is auth -> role -> rate
	// limit, matching every other role-gated route (D6), so a forbidden role
	// never burns rate-limit quota.
	mux.Handle("GET /api/v1/audit",
		s.requireAuth(s.requireRole(domain.RoleAdmin)(s.rateLimit(120, time.Minute)(http.HandlerFunc(auditHandler.Latest)))))

	return s.chain(mux)
}

// requireAuth applies the JWT middleware bound to this server's token manager.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return RequireAuth(s.tokens)(next)
}

// requireRole applies the RBAC middleware with the given allowed role codes
// (D6/D7). It must wrap a requireAuth-guarded handler, since it reads the role
// claim from the request context.
func (s *Server) requireRole(allowed ...string) func(http.Handler) http.Handler {
	return RequireRole(allowed...)
}

// rateLimit applies the rate-limit middleware bound to this server's limiter.
// A nil limiter makes it a no-op (rate limiting disabled). On a 429 the
// middleware emits the rate-limit-exceeded breach event (audit warn when the
// request is authenticated, slog-only when anonymous).
func (s *Server) rateLimit(limit int, window time.Duration) func(http.Handler) http.Handler {
	return rateLimit(s.rateLimiter, limit, window, s.emitRateLimitExceeded)
}

// emitRateLimitExceeded emits the rate-limit breach signal through the same
// sink the services use — ONE emission path (R3). The 429 middleware runs
// after RequireAuth on protected routes, so authenticated requests carry
// claims -> tenant; anonymous auth-route hits have no tenant -> Anonymous=true
// so the audit sink skips the row (slog-only under RLS).
func (s *Server) emitRateLimitExceeded(r *http.Request) {
	tenantID := claims.TenantIDFrom(r.Context())
	userID := claims.UserIDFrom(r.Context())
	anonymous := tenantID == ""

	services.EmitSignal(r.Context(), s.signals, services.SignalRateLimitExceeded, anonymous,
		tenantID, userID, requestid.FromRequestID(r.Context()))
}

// chain applies global middleware so panics are caught and every log line is
// correlated by request id: logging outermost, recovery innermost.
func (s *Server) chain(next http.Handler) http.Handler {
	return s.logging(s.recover(next))
}
