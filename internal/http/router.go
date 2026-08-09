// Package http wires the stdlib HTTP server: a Go 1.22+ ServeMux, slog
// middleware and the handlers.
package http

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/ezequielranieri/agro-iam/internal/application/ports"
	"github.com/ezequielranieri/agro-iam/internal/http/handlers"
	"github.com/ezequielranieri/agro-iam/internal/infrastructure/redis"
)

// Server bundles every dependency the HTTP layer needs.
type Server struct {
	auth        ports.AuthService
	tokens      ports.TokenManager
	lots        ports.LotService
	rateLimiter *redis.RateLimiter
	log         *slog.Logger
}

// NewServer builds the server with its dependencies.
// rateLimiter may be nil (rate limiting disabled).
func NewServer(auth ports.AuthService, tokens ports.TokenManager, lots ports.LotService, rateLimiter *redis.RateLimiter, log *slog.Logger) *Server {
	return &Server{auth: auth, tokens: tokens, lots: lots, rateLimiter: rateLimiter, log: log}
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

	return s.chain(mux)
}

// requireAuth applies the JWT middleware bound to this server's token manager.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return RequireAuth(s.tokens)(next)
}

// rateLimit applies the rate-limit middleware bound to this server's limiter.
// A nil limiter makes it a no-op (rate limiting disabled).
func (s *Server) rateLimit(limit int, window time.Duration) func(http.Handler) http.Handler {
	return rateLimit(s.rateLimiter, limit, window)
}

// chain applies global middleware so panics are caught and every log line is
// correlated by request id: logging outermost, recovery innermost.
func (s *Server) chain(next http.Handler) http.Handler {
	return s.logging(s.recover(next))
}
