// Redis-backed rate limiter integration tests (PR2-T5).
// They run only when TEST_REDIS_ADDR is set (e.g. the docker-compose redis
// container); otherwise they skip, keeping `go test ./...` green without Redis.
package redis

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// newIntegrationLimiter builds a limiter over the real Redis from
// TEST_REDIS_ADDR, or skips when the env var is unset. It returns the
// freshly-cleared unique key the caller MUST use, so a prior failed run can
// never poison the bucket: every limiter.Allow call goes through that same key.
func newIntegrationLimiter(t *testing.T) (*RateLimiter, string) {
	t.Helper()
	addr := os.Getenv("TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("TEST_REDIS_ADDR not set; skipping redis integration tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	client := redis.NewClient(&redis.Options{Addr: addr})
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		t.Skipf("redis not reachable at %s: %v", addr, err)
	}
	t.Cleanup(func() { _ = client.Close() })

	// Unique key per test so a prior failed run cannot poison the bucket.
	key := "it:" + t.Name()
	client.Del(ctx, key)
	t.Cleanup(func() { client.Del(context.Background(), key) })

	return NewRateLimiter(client, nil), key
}

// TestRateLimiterRedis_LuaIncrPexpire proves the Lua INCR+PEXPIRE path against
// real Redis: the counter increments and the key expires after the window.
func TestRateLimiterRedis_LuaIncrPexpire(t *testing.T) {
	limiter, key := newIntegrationLimiter(t)
	defer limiter.Close()

	for i := 0; i < 5; i++ {
		res := limiter.Allow(key, 5, time.Minute)
		if !res.Allowed {
			t.Fatalf("request %d: expected allowed, got denied", i+1)
		}
	}
	// 6th denied with a Retry-After.
	res := limiter.Allow(key, 5, time.Minute)
	if res.Allowed {
		t.Fatal("6th request: expected denied")
	}
	if res.RetryAfter <= 0 {
		t.Fatalf("6th request: RetryAfter = %v, want > 0", res.RetryAfter)
	}
}

// TestRateLimiterRedis_WindowRotation proves the fixed window rotates: after
// the window elapses the same key gets a fresh quota.
func TestRateLimiterRedis_WindowRotation(t *testing.T) {
	limiter, key := newIntegrationLimiter(t)
	defer limiter.Close()

	if res := limiter.Allow(key, 2, 200*time.Millisecond); !res.Allowed {
		t.Fatal("request 1: expected allowed")
	}
	if res := limiter.Allow(key, 2, 200*time.Millisecond); !res.Allowed {
		t.Fatal("request 2: expected allowed")
	}
	if res := limiter.Allow(key, 2, 200*time.Millisecond); res.Allowed {
		t.Fatal("request 3: expected denied")
	}

	time.Sleep(250 * time.Millisecond)

	if res := limiter.Allow(key, 2, 200*time.Millisecond); !res.Allowed {
		t.Fatal("request after rotation: expected allowed")
	}
}
