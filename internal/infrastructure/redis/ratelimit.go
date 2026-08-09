// Package redis — rate limiting with Lua (Redis) + in-memory fallback.
// Redis is optional: nil client -> in-memory only (fail-open with WARN log).
package redis

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// RateLimitResult is the outcome of an Allow check.
type RateLimitResult struct {
	Allowed    bool
	RetryAfter time.Duration
}

// RateLimiter provides fixed-window rate limiting.
// If redisClient is nil, uses in-memory fallback (per-process, single-instance).
// On Redis errors, fails open (allows) with WARN log.
type RateLimiter struct {
	client *redis.Client
	log    *slog.Logger
	// In-memory fallback state
	mu    sync.Mutex
	buckets map[string]*tokenBucket
}

type tokenBucket struct {
	count     int
	windowEnd time.Time
}

// NewRateLimiter creates a limiter. Pass nil client for in-memory only.
// Pass nil logger for discard.
func NewRateLimiter(client *redis.Client, log *slog.Logger) *RateLimiter {
	if log == nil {
		log = slog.New(slog.NewTextHandler(nil, nil))
	}
	return &RateLimiter{
		client:  client,
		log:     log,
		buckets: make(map[string]*tokenBucket),
	}
}

// Allow checks if a request is allowed for the given key, limit, and window.
// limit <= 0 means unlimited.
func (r *RateLimiter) Allow(key string, limit int, window time.Duration) RateLimitResult {
	if limit <= 0 {
		return RateLimitResult{Allowed: true, RetryAfter: 0}
	}

	if r.client != nil {
		return r.allowRedis(key, limit, window)
	}
	return r.allowMemory(key, limit, window)
}

// allowMemory is the in-memory fixed-window implementation.
func (r *RateLimiter) allowMemory(key string, limit int, window time.Duration) RateLimitResult {
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()

	b := r.buckets[key]
	if b == nil || now.After(b.windowEnd) {
		// New or expired window
		r.buckets[key] = &tokenBucket{
			count:     1,
			windowEnd: now.Add(window),
		}
		return RateLimitResult{Allowed: true, RetryAfter: 0}
	}

	b.count++
	if b.count <= limit {
		return RateLimitResult{Allowed: true, RetryAfter: 0}
	}

	// Exceeded
	retryAfter := time.Until(b.windowEnd)
	if retryAfter < 0 {
		retryAfter = 0
	}
	return RateLimitResult{Allowed: false, RetryAfter: retryAfter}
}

// allowRedis uses Redis EVAL with Lua script for atomic INCR + PEXPIRE.
// Falls back to in-memory on error (fail-open with WARN).
func (r *RateLimiter) allowRedis(key string, limit int, window time.Duration) RateLimitResult {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	// Lua script: INCR + PEXPIRE (set expiry only on first request in window)
	// KEYS[1] = key, ARGV[1] = limit, ARGV[2] = window ms
	script := redis.NewScript(`
		local current = redis.call('INCR', KEYS[1])
		if current == 1 then
			redis.call('PEXPIRE', KEYS[1], ARGV[2])
		end
		local ttl = redis.call('PTTL', KEYS[1])
		return {current, ttl}
	`)

	result, err := script.Run(ctx, r.client, []string{key}, limit, int(window.Milliseconds())).Slice()
	if err != nil {
		r.log.Warn("rate limiter redis error, failing open to in-memory", "error", err, "key", key)
		return r.allowMemory(key, limit, window)
	}

	current := int(result[0].(int64))
	ttlMs := int64(result[1].(int64))

	if current <= limit {
		return RateLimitResult{Allowed: true, RetryAfter: 0}
	}

	retryAfter := time.Duration(ttlMs) * time.Millisecond
	if retryAfter < 0 {
		retryAfter = 0
	}
	return RateLimitResult{Allowed: false, RetryAfter: retryAfter}
}

// Close releases resources (closes Redis client if owned).
func (r *RateLimiter) Close() error {
	if r.client != nil {
		return r.client.Close()
	}
	return nil
}