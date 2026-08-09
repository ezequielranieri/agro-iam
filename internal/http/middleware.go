package http

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ezequielranieri/agro-iam/internal/application/ports"
	"github.com/ezequielranieri/agro-iam/internal/http/claims"
	"github.com/ezequielranieri/agro-iam/internal/infrastructure/redis"
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

// rateLimit returns middleware that enforces a fixed-window rate limit.
// The key derivation depends on the route:
//   - /healthz: exempt (no limit)
//   - /api/v1/auth/login, /api/v1/auth/refresh: rl:auth:{ip} (strip port)
//   - Authenticated API routes (after RequireAuth): rl:api:{tenant}:{user}
// If limiter is nil, rate limiting is disabled (no-op).
func rateLimit(limiter *redis.RateLimiter, limit int, window time.Duration) func(http.Handler) http.Handler {
	if limiter == nil {
		return func(next http.Handler) http.Handler {
			return next
		}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Healthz is always exempt
			if r.URL.Path == "/healthz" {
				next.ServeHTTP(w, r)
				return
			}

			key := deriveRateLimitKey(r)
			if key == "" {
				// Should not happen for configured routes, but fail open
				next.ServeHTTP(w, r)
				return
			}

			res := limiter.Allow(key, limit, window)
			if !res.Allowed {
				w.Header().Set("Retry-After", formatRetryAfter(res.RetryAfter))
				writeError(w, http.StatusTooManyRequests, "rate limited")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// deriveRateLimitKey returns the rate limit key for the request.
// Returns empty string if the route is not configured for rate limiting.
func deriveRateLimitKey(r *http.Request) string {
	path := r.URL.Path
	ip := stripPort(r.RemoteAddr)

	// Auth routes: per-IP
	if path == "/api/v1/auth/login" || path == "/api/v1/auth/refresh" {
		return "rl:auth:" + ip
	}

	// Authenticated API routes: per tenant:user (claims injected by RequireAuth)
	if strings.HasPrefix(path, "/api/v1/") {
		tenantID := claims.TenantIDFrom(r.Context())
		userID := claims.UserIDFrom(r.Context())
		if tenantID != "" && userID != "" {
			return "rl:api:" + tenantID + ":" + userID
		}
		// No claims yet (e.g., unauthenticated request to protected route) -
		// RequireAuth will reject before rate limit runs, so this shouldn't happen.
		// Fail open.
		return ""
	}

	// Other routes: no rate limit configured
	return ""
}

// stripPort removes the port from a "host:port" RemoteAddr.
// IPv6 addresses in brackets are handled by splitting on the last ']:' or ':'.
func stripPort(addr string) string {
	// IPv6: [::1]:port or [2001:db8::1]:port
	if strings.HasPrefix(addr, "[") {
		if i := strings.LastIndex(addr, "]:"); i != -1 {
			return addr[:i+1] // keep brackets
		}
		return addr
	}
	// IPv4: host:port
	if i := strings.LastIndex(addr, ":"); i != -1 {
		return addr[:i]
	}
	return addr
}

// formatRetryAfter formats a duration as seconds (ceiling) for the
// Retry-After header. A zero/negative duration is treated as 1 second so the
// header is always present on a 429.
func formatRetryAfter(d time.Duration) string {
	secs := int((d + time.Second - 1) / time.Second)
	if secs <= 0 {
		secs = 1
	}
	return strconv.Itoa(secs)
}
