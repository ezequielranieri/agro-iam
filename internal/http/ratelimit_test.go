// Package http — rate limit middleware tests (PR2-T3 RED phase).
// These tests define the expected HTTP behavior before the middleware exists.
package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/ezequielranieri/agro-iam/internal/http/claims"
	"github.com/ezequielranieri/agro-iam/internal/infrastructure/redis"
)

// newTestRateLimiter returns an in-memory rate limiter for testing.
func newTestRateLimiter(t *testing.T) *redis.RateLimiter {
	t.Helper()
	return redis.NewRateLimiter(nil, nil)
}

// TestRateLimit_HealthzExempt verifies /healthz is never rate limited.
func TestRateLimit_HealthzExempt(t *testing.T) {
	limiter := newTestRateLimiter(t)
	defer limiter.Close()

	stub := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Even with limit=1, healthz should always pass
	for i := 0; i < 10; i++ {
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		req.RemoteAddr = "127.0.0.1:12345"
		rec := httptest.NewRecorder()

		rateLimit(limiter, 1, time.Minute)(stub).ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: healthz status = %d, want 200", i+1, rec.Code)
		}
	}
}

// TestRateLimit_AuthRouteLogin verifies login route: 5 req/min per IP.
func TestRateLimit_AuthRouteLogin(t *testing.T) {
	limiter := newTestRateLimiter(t)
	defer limiter.Close()

	stub := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := rateLimit(limiter, 5, time.Minute)(stub)

	// First 5 allowed
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
		req.RemoteAddr = "192.168.1.100:54321"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want 200", i+1, rec.Code)
		}
	}

	// 6th denied with 429
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	req.RemoteAddr = "192.168.1.100:54321"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("6th request: status = %d, want 429", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("6th request: missing Retry-After header")
	}
	if rec.Body.String() != "{\"error\":\"rate limited\"}\n" {
		t.Fatalf("6th request: body = %q, want rate limited JSON", rec.Body.String())
	}
}

// TestRateLimit_AuthRouteRefresh verifies refresh route: 30 req/min per IP.
func TestRateLimit_AuthRouteRefresh(t *testing.T) {
	limiter := newTestRateLimiter(t)
	defer limiter.Close()

	stub := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := rateLimit(limiter, 30, time.Minute)(stub)

	// First 30 allowed
	for i := 0; i < 30; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", nil)
		req.RemoteAddr = "10.0.0.5:12345"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want 200", i+1, rec.Code)
		}
	}

	// 31st denied
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", nil)
	req.RemoteAddr = "10.0.0.5:12345"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("31st request: status = %d, want 429", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("31st request: missing Retry-After header")
	}
}

// TestRateLimit_APIRoute verifies authenticated API routes: 120 req/min per tenant:user.
func TestRateLimit_APIRoute(t *testing.T) {
	limiter := newTestRateLimiter(t)
	defer limiter.Close()

	stub := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := rateLimit(limiter, 120, time.Minute)(stub)

	// Simulate authenticated context with claims
	ctx := claims.WithIdentity(
		context.Background(),
		"user-1", "tenant-1", "admin",
	)

	// First 120 allowed
	for i := 0; i < 120; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/lots", nil).
			WithContext(ctx)
		req.RemoteAddr = "192.168.1.1:54321"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want 200", i+1, rec.Code)
		}
	}

	// 121st denied
	req := httptest.NewRequest(http.MethodGet, "/api/v1/lots", nil).
		WithContext(ctx)
	req.RemoteAddr = "192.168.1.1:54321"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("121st request: status = %d, want 429", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("121st request: missing Retry-After header")
	}
}

// TestRateLimit_KeyDerivation_stripPort verifies IP key strips port.
func TestRateLimit_KeyDerivation_stripPort(t *testing.T) {
	limiter := newTestRateLimiter(t)
	defer limiter.Close()

	stub := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := rateLimit(limiter, 2, time.Minute)(stub)

	// Same IP, different ports = same key (port stripped)
	req1 := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	req1.RemoteAddr = "192.168.1.100:1111"
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)

	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	req2.RemoteAddr = "192.168.1.100:2222"
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)

	// 3rd request from same IP (different port) should be denied
	req3 := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	req3.RemoteAddr = "192.168.1.100:3333"
	rec3 := httptest.NewRecorder()
	handler.ServeHTTP(rec3, req3)

	if rec1.Code != http.StatusOK || rec2.Code != http.StatusOK {
		t.Fatal("first two requests should be allowed")
	}
	if rec3.Code != http.StatusTooManyRequests {
		t.Fatalf("3rd request (same IP diff port): status = %d, want 429", rec3.Code)
	}
}

// TestRateLimit_KeyDerivation_authRateKey verifies auth routes use rl:auth:{ip}.
func TestRateLimit_KeyDerivation_authRateKey(t *testing.T) {
	limiter := newTestRateLimiter(t)
	defer limiter.Close()

	stub := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := rateLimit(limiter, 1, time.Minute)(stub)

	// First request allowed
	req1 := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	req1.RemoteAddr = "203.0.113.50:443"
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)

	// Second denied
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	req2.RemoteAddr = "203.0.113.50:443"
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)

	if rec1.Code != http.StatusOK {
		t.Fatal("first should be allowed")
	}
	if rec2.Code != http.StatusTooManyRequests {
		t.Fatal("second should be denied (auth key)")
	}
}

// TestRateLimit_KeyDerivation_apiRateKey verifies API routes use rl:api:{tenant}:{user}.
func TestRateLimit_KeyDerivation_apiRateKey(t *testing.T) {
	limiter := newTestRateLimiter(t)
	defer limiter.Close()

	stub := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := rateLimit(limiter, 1, time.Minute)(stub)

	ctx := claims.WithIdentity(context.Background(), "user-A", "tenant-X", "admin")

	req1 := httptest.NewRequest(http.MethodGet, "/api/v1/lots", nil).WithContext(ctx)
	req1.RemoteAddr = "192.168.1.1:1111"
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)

	// Different user, same tenant = different key
	ctx2 := claims.WithIdentity(context.Background(), "user-B", "tenant-X", "admin")
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/lots", nil).WithContext(ctx2)
	req2.RemoteAddr = "192.168.1.1:2222"
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)

	// Both should be allowed (different keys)
	if rec1.Code != http.StatusOK || rec2.Code != http.StatusOK {
		t.Fatal("both users should have independent quotas")
	}

	// Same user, same tenant = same key (denied on 2nd)
	req3 := httptest.NewRequest(http.MethodGet, "/api/v1/lots", nil).WithContext(ctx)
	req3.RemoteAddr = "192.168.1.1:3333"
	rec3 := httptest.NewRecorder()
	handler.ServeHTTP(rec3, req3)

	if rec3.Code != http.StatusTooManyRequests {
		t.Fatal("same user+tenant should share quota")
	}
}

// TestRateLimit_PerRouteAfterRequireAuth verifies rate limit runs AFTER auth.
func TestRateLimit_PerRouteAfterRequireAuth(t *testing.T) {
	// This is an integration-style test: the router chains auth then rate limit.
	// We verify by checking that an unauthenticated request to /api/v1/lots
	// gets 401 (auth) not 429 (rate limit).
	limiter := newTestRateLimiter(t)
	defer limiter.Close()

	tm := newTestTokenManager(t)

	// Build a mini router: auth -> rate limit -> handler
	authHandler := RequireAuth(tm)
	rateHandler := rateLimit(limiter, 100, time.Minute)

	stub := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Chain: auth -> rate limit -> stub
	chained := authHandler(rateHandler(stub))

	// Unauthenticated request -> 401 (not 429)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/lots", nil)
	req.RemoteAddr = "192.168.1.1:1111"
	rec := httptest.NewRecorder()
	chained.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated: status = %d, want 401 (auth before rate limit)", rec.Code)
	}
}

// TestRateLimit_Concurrency verifies thread-safety under concurrent HTTP requests.
func TestRateLimit_Concurrency(t *testing.T) {
	limiter := newTestRateLimiter(t)
	defer limiter.Close()

	stub := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := rateLimit(limiter, 100, time.Minute)(stub)

	const goroutines = 150
	var wg sync.WaitGroup
	allowed := 0
	denied := 0
	var mu sync.Mutex

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
			req.RemoteAddr = "192.168.1.100:12345"
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			mu.Lock()
			if rec.Code == http.StatusOK {
				allowed++
			} else if rec.Code == http.StatusTooManyRequests {
				denied++
			}
			mu.Unlock()
		}()
	}
	wg.Wait()

	if allowed != 100 {
		t.Fatalf("allowed=%d, want 100", allowed)
	}
	if denied != goroutines-100 {
		t.Fatalf("denied=%d, want %d", denied, goroutines-100)
	}
}