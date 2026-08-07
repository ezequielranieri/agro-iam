// Package redis provides a go-redis client factory. Redis is used for
// session/rate-limit concerns in later slices; the factory exists now so the
// wiring in cmd/api does not change later.
package redis

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// NewClient creates a redis client and verifies connectivity with Ping.
// Callers should treat a failure as non-fatal during startup (log + continue).
func NewClient(ctx context.Context, addr string) (*redis.Client, error) {
	client := redis.NewClient(&redis.Options{Addr: addr})

	pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		_ = client.Close()
		return nil, err
	}
	return client, nil
}
