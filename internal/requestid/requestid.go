// Package requestid provides a request correlation id: a random opaque id
// injected into the request context by the HTTP layer and carried into logs
// and audit emission, so a security event can be tied to the exact request
// that produced it.
package requestid

import (
	"context"
	"crypto/rand"
	"encoding/hex"
)

type ctxKey int

const key ctxKey = 0

// NewID returns a 32-char hex request id from crypto/rand. On entropy failure
// it returns the literal "unknown" so callers never crash.
func NewID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "unknown"
	}
	return hex.EncodeToString(b)
}

// WithRequestID returns a context carrying the given request id.
func WithRequestID(parent context.Context, id string) context.Context {
	return context.WithValue(parent, key, id)
}

// FromRequestID returns the request id stored in the context, or "" when the
// context has none (e.g. a background task).
func FromRequestID(ctx context.Context) string {
	id, _ := ctx.Value(key).(string)
	return id
}
