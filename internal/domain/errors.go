package domain

import "errors"

// Sentinel domain errors. Keeping them in the domain package means the whole
// codebase — application services, repositories, HTTP handlers — can reason
// about failures using stable values that never leak SQL or framework details.

var (
	// ErrNotFound is returned when a requested entity does not exist.
	ErrNotFound = errors.New("not found")
	// ErrConflict is returned when an insert would violate a uniqueness invariant.
	ErrConflict = errors.New("conflict")
	// ErrInvalid is returned when an entity fails its validation rules.
	ErrInvalid = errors.New("invalid argument")
	// ErrInvalidInput is returned when a request payload fails validation.
	ErrInvalidInput = errors.New("invalid input")
	// ErrUnauthorized is returned when credentials are wrong or a token is invalid.
	ErrUnauthorized = errors.New("unauthorized")
	// ErrForbidden is returned when an authenticated user lacks permission.
	ErrForbidden = errors.New("forbidden")
	// ErrTenantRequired is returned when an operation needs a tenant session.
	ErrTenantRequired = errors.New("tenant context required")
)
