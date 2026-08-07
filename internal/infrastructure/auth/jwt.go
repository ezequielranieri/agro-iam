// Package auth contains the concrete security primitives: JWT access tokens,
// Argon2id password hashing and refresh-token persistence. Each type satisfies
// the matching port from application/ports.
package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/ezequielranieri/agro-iam/internal/application/ports"
	"github.com/ezequielranieri/agro-iam/internal/domain"
)

// TokenManager issues and verifies HS256 JWTs with a short TTL (15 minutes by
// default). HS256 is symmetric: the secret signs AND verifies, so JWT_SECRET
// must stay out of the repo and ideally be rotated on a schedule.
type TokenManager struct {
	secret []byte
	ttl    time.Duration
	now    func() time.Time
}

// NewTokenManager builds a JWT signer. An empty secret is rejected loudly
// rather than silently minting forged tokens.
func NewTokenManager(secret string, ttl time.Duration) (*TokenManager, error) {
	if secret == "" {
		return nil, errors.New("auth: JWT secret must not be empty")
	}
	return &TokenManager{secret: []byte(secret), ttl: ttl, now: time.Now}, nil
}

// Issue mints a signed token carrying sub, tenant_id and role. The token is
// stateless by design: verifying it only needs the shared secret.
func (m *TokenManager) Issue(claims ports.TokenClaims) (string, error) {
	now := m.now()
	claimsMap := jwt.MapClaims{
		"sub":       claims.UserID,
		"tenant_id": claims.TenantID,
		"role":      claims.Role,
		"iat":       now.Unix(),
		"exp":       now.Add(m.ttl).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claimsMap)
	return token.SignedString(m.secret)
}

// Verify parses, validates signature and expiry, then returns the claims.
func (m *TokenManager) Verify(tokenString string) (*ports.TokenClaims, error) {
	token, err := jwt.Parse(tokenString, func(t *jwt.Token) (any, error) {
		// Only HS256 is accepted; rejecting other algorithms prevents the
		// classic "alg=none" or asymmetric-key confusion attacks.
		if t.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("auth: unexpected signing method %v", t.Method)
		}
		return m.secret, nil
	})
	if err != nil {
		return nil, domain.ErrUnauthorized
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || !token.Valid {
		return nil, domain.ErrUnauthorized
	}

	sub, _ := claims["sub"].(string)
	tenantID, _ := claims["tenant_id"].(string)
	role, _ := claims["role"].(string)
	if sub == "" || tenantID == "" {
		return nil, domain.ErrUnauthorized
	}

	return &ports.TokenClaims{UserID: sub, TenantID: tenantID, Role: role}, nil
}

var _ ports.TokenManager = (*TokenManager)(nil)
