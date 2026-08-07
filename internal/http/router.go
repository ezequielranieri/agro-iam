// Package http wires the stdlib HTTP server: a Go 1.22+ ServeMux, slog
// middleware and the handlers.
package http

import (
	"log/slog"
	"net/http"

	"github.com/ezequielranieri/agro-iam/internal/application/ports"
	"github.com/ezequielranieri/agro-iam/internal/http/handlers"
)

// Server bundles every dependency the HTTP layer needs.
type Server struct {
	auth   ports.AuthService
	tokens ports.TokenManager
	lots   ports.LotService
	log    *slog.Logger
}

// NewServer builds the server with its dependencies.
func NewServer(auth ports.AuthService, tokens ports.TokenManager, lots ports.LotService, log *slog.Logger) *Server {
	return &Server{auth: auth, tokens: tokens, lots: lots, log: log}
}

// Routes assembles the stdlib ServeMux. Path parameters use the Go 1.22
// `{param}` syntax â€” no third-party router is needed. Health and auth routes
// are public; lot routes are wrapped with s.requireAuth so the authenticated
// tenant is always present in the request context before a handler runs.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	authHandler := handlers.NewAuthHandler(s.auth, s.log)
	mux.HandleFunc("GET /healthz", handlers.Health)
	mux.HandleFunc("POST /api/v1/auth/login", authHandler.Login)
	mux.HandleFunc("POST /api/v1/auth/refresh", authHandler.Refresh)

	lotsHandler := handlers.NewLotsHandler(s.lots, s.log)
	mux.Handle("GET /api/v1/lots", s.requireAuth(http.HandlerFunc(lotsHandler.List)))
	mux.Handle("POST /api/v1/lots", s.requireAuth(http.HandlerFunc(lotsHandler.Create)))

	return s.chain(mux)
}

// requireAuth applies the JWT middleware bound to this server's token manager.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return RequireAuth(s.tokens)(next)
}

// chain applies global middleware so panics are caught and every log line is
// correlated by request id: logging outermost, recovery innermost.
func (s *Server) chain(next http.Handler) http.Handler {
	return s.logging(s.recover(next))
}
