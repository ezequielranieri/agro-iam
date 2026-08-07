package domain

import (
	"regexp"
	"time"
)

var emailPattern = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

// User belongs to exactly one tenant. Every user row is RLS-protected by its
// tenant_id, so one query cannot ever see another tenant's users.
type User struct {
	ID           string
	TenantID     string
	Email        string
	PasswordHash string
	FullName     string
	IsActive     bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// IsValid checks structural invariants. Password strength is a policy concern
// that belongs in the application layer; here we only require a hash exists.
func (u User) IsValid() bool {
	return u.TenantID != "" &&
		emailPattern.MatchString(u.Email) &&
		u.PasswordHash != "" &&
		u.FullName != ""
}
