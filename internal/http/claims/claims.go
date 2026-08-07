// Package claims owns the authenticated identity carried in the request
// context. It is its own package so both the auth middleware (package http) and
// the handlers (package http/handlers) can share the same typed context keys
// without an import cycle — http already imports handlers, so handlers cannot
// import http back.
package claims

import "context"

type ctxKey int

const (
	userIDKey ctxKey = iota
	tenantIDKey
	roleKey
)

// WithIdentity returns a context carrying the authenticated user's identity as
// three typed values. Strings are used deliberately: the claims may legitimately
// be empty (e.g. a roleless user), which is indistinguishable from "absent" to
// the accessors below — callers must decide which case they care about.
func WithIdentity(ctx context.Context, userID, tenantID, role string) context.Context {
	ctx = context.WithValue(ctx, userIDKey, userID)
	ctx = context.WithValue(ctx, tenantIDKey, tenantID)
	ctx = context.WithValue(ctx, roleKey, role)
	return ctx
}

// UserIDFrom returns the authenticated user's id, or "" when the middleware has
// not injected any claims.
func UserIDFrom(ctx context.Context) string {
	v, _ := ctx.Value(userIDKey).(string)
	return v
}

// TenantIDFrom returns the authenticated tenant's id, or "" when the middleware
// has not injected any claims. The lot handlers use this to scope every query.
func TenantIDFrom(ctx context.Context) string {
	v, _ := ctx.Value(tenantIDKey).(string)
	return v
}

// RoleFrom returns the authenticated user's role, or "" when absent.
func RoleFrom(ctx context.Context) string {
	v, _ := ctx.Value(roleKey).(string)
	return v
}
