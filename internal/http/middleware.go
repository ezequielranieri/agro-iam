package http

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/ezequielranieri/agro-iam/internal/application/ports"
	"github.com/ezequielranieri/agro-iam/internal/http/claims"
)

type ctxKey int

const requestIDKey ctxKey = 0

// logging wraps every request with a structured log line: method, path, status,
// duration and a request id for correlation.
func (s *Server) logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		r = r.WithContext(requestIDContext(r.Context()))
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)

		s.log.Info("http",
			"method", r.Method,
			"path", r.URL.Path,
			"status", sw.status,
			"duration_ms", time.Since(start).Milliseconds(),
			"remote", r.RemoteAddr,
		)
	})
}

// recover converts panics in handlers into a 500 instead of crashing the server.
func (s *Server) recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.log.Error("http panic", "panic", rec)
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// statusWriter records the response status code for the access log. The default
// ResponseWriter does not expose it; this thin wrapper does.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// requestIDContext returns a context carrying a fresh random request id, used
// to correlate logs across a single request. Uses crypto/rand to stay within
// the stdlib â€” no third-party UUID library.
func requestIDContext(parent context.Context) context.Context {
	return context.WithValue(parent, requestIDKey, newRequestID())
}

// newRequestID returns a 32-char hex string from crypto/rand.
func newRequestID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "unknown"
	}
	return hex.EncodeToString(b)
}

// RequireAuth guards a handler behind a valid `Authorization: Bearer <token>`
// header. It verifies the JWT, then injects the claims into the request context
// so downstream handlers scope every query to the authenticated tenant. Every
// failure â€” missing header, wrong scheme, invalid or expired token â€” collapses
// to the same 401 JSON body, so an attacker cannot tell which check failed.
func RequireAuth(tokenManager ports.TokenManager) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := bearerToken(r.Header.Get("Authorization"))
			if !ok {
				writeError(w, http.StatusUnauthorized, "unauthorized")
				return
			}

			tokenClaims, err := tokenManager.Verify(token)
			if err != nil {
				writeError(w, http.StatusUnauthorized, "unauthorized")
				return
			}

			ctx := claims.WithIdentity(r.Context(), tokenClaims.UserID, tokenClaims.TenantID, tokenClaims.Role)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// bearerToken extracts the token from an Authorization header, requiring the
// exact `Bearer ` scheme. Anything else â€” empty header, wrong scheme, missing
// token â€” is rejected outright; there is no fallback scheme.
func bearerToken(header string) (string, bool) {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) || len(header) <= len(prefix) {
		return "", false
	}
	return header[len(prefix):], true
}
