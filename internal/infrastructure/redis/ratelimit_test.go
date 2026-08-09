// Package redis — in-memory rate limiter tests (PR2-T1 RED phase).
// These tests define the expected behavior before the implementation exists.
package redis

import (
	"sync"
	"testing"
	"time"
)

// TestInMemoryRateLimiter_BelowLimit verifies requests under the limit are allowed.
func TestInMemoryRateLimiter_BelowLimit(t *testing.T) {
	limiter := NewRateLimiter(nil, nil) // nil client, nil logger -> in-memory
	defer limiter.Close()

	for i := 0; i < 5; i++ {
		res := limiter.Allow("test-key", 5, time.Minute)
		if !res.Allowed {
			t.Fatalf("request %d: expected allowed, got denied", i+1)
		}
		if res.RetryAfter > 0 {
			t.Fatalf("request %d: expected RetryAfter=0, got %v", i+1, res.RetryAfter)
		}
	}
}

// TestInMemoryRateLimiter_ExceedsLimit verifies requests over the limit are denied with RetryAfter.
func TestInMemoryRateLimiter_ExceedsLimit(t *testing.T) {
	limiter := NewRateLimiter(nil, nil)
	defer limiter.Close()

	// First 5 allowed
	for i := 0; i < 5; i++ {
		res := limiter.Allow("test-key", 5, time.Minute)
		if !res.Allowed {
			t.Fatalf("request %d: expected allowed, got denied", i+1)
		}
	}

	// 6th should be denied
	res := limiter.Allow("test-key", 5, time.Minute)
	if res.Allowed {
		t.Fatal("6th request: expected denied, got allowed")
	}
	if res.RetryAfter <= 0 {
		t.Fatalf("6th request: expected RetryAfter>0, got %v", res.RetryAfter)
	}
}

// TestInMemoryRateLimiter_WindowRotation verifies the sliding window resets after expiry.
func TestInMemoryRateLimiter_WindowRotation(t *testing.T) {
	limiter := NewRateLimiter(nil, nil)
	defer limiter.Close()

	// Use a very short window for testing
	window := 50 * time.Millisecond

	// Exhaust the limit
	for i := 0; i < 3; i++ {
		res := limiter.Allow("rotation-key", 3, window)
		if !res.Allowed {
			t.Fatalf("request %d: expected allowed, got denied", i+1)
		}
	}

	// Next request denied
	res := limiter.Allow("rotation-key", 3, window)
	if res.Allowed {
		t.Fatal("expected denied before window expiry")
	}

	// Wait for window to rotate
	time.Sleep(window + 10*time.Millisecond)

	// Should be allowed again
	res = limiter.Allow("rotation-key", 3, window)
	if !res.Allowed {
		t.Fatal("expected allowed after window rotation")
	}
	if res.RetryAfter > 0 {
		t.Fatalf("expected RetryAfter=0 after rotation, got %v", res.RetryAfter)
	}
}

// TestInMemoryRateLimiter_Concurrency verifies thread-safety under concurrent access.
func TestInMemoryRateLimiter_Concurrency(t *testing.T) {
	limiter := NewRateLimiter(nil, nil)
	defer limiter.Close()

	const (
		goroutines = 150
		limit      = 100
	)
	var wg sync.WaitGroup
	allowed := 0
	denied := 0
	var mu sync.Mutex

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res := limiter.Allow("concurrent-key", limit, time.Minute)
			mu.Lock()
			if res.Allowed {
				allowed++
			} else {
				denied++
			}
			mu.Unlock()
		}()
	}
	wg.Wait()

	// Exactly 'limit' should be allowed, rest denied
	if allowed != limit {
		t.Fatalf("allowed=%d, want %d", allowed, limit)
	}
	if denied != goroutines-limit {
		t.Fatalf("denied=%d, want %d", denied, goroutines-limit)
	}
}

// TestInMemoryRateLimiter_DifferentKeysAreIndependent verifies keys don't interfere.
func TestInMemoryRateLimiter_DifferentKeysAreIndependent(t *testing.T) {
	limiter := NewRateLimiter(nil, nil)
	defer limiter.Close()

	// Exhaust key A
	for i := 0; i < 3; i++ {
		res := limiter.Allow("key-A", 3, time.Minute)
		if !res.Allowed {
			t.Fatalf("key-A request %d: expected allowed", i+1)
		}
	}
	res := limiter.Allow("key-A", 3, time.Minute)
	if res.Allowed {
		t.Fatal("key-A 4th: expected denied")
	}

	// Key B should still have full quota
	res = limiter.Allow("key-B", 3, time.Minute)
	if !res.Allowed {
		t.Fatal("key-B: expected allowed (independent quota)")
	}
}

// TestInMemoryRateLimiter_ZeroLimitMeansUnlimited is a design decision test.
func TestInMemoryRateLimiter_ZeroLimitMeansUnlimited(t *testing.T) {
	limiter := NewRateLimiter(nil, nil)
	defer limiter.Close()

	// Limit 0 = unlimited
	for i := 0; i < 1000; i++ {
		res := limiter.Allow("unlimited-key", 0, time.Minute)
		if !res.Allowed {
			t.Fatalf("request %d: expected allowed with limit=0", i+1)
		}
	}
}

// TestInMemoryRateLimiter_NegativeLimitTreatedAsZero is a defensive test.
func TestInMemoryRateLimiter_NegativeLimitTreatedAsZero(t *testing.T) {
	limiter := NewRateLimiter(nil, nil)
	defer limiter.Close()

	res := limiter.Allow("negative-key", -1, time.Minute)
	if !res.Allowed {
		t.Fatal("negative limit should be treated as unlimited (allowed)")
	}
}