package services

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ezequielranieri/agro-iam/internal/application/ports"
	"github.com/ezequielranieri/agro-iam/internal/domain"
)

// recordingTokenManager captures every issued claim set so tests can assert the
// role embedded at token-issue time (D3).
type recordingTokenManager struct {
	issued []ports.TokenClaims
}

func (m *recordingTokenManager) Issue(claims ports.TokenClaims) (string, error) {
	m.issued = append(m.issued, claims)
	return "access-token", nil
}
func (m *recordingTokenManager) Verify(token string) (*ports.TokenClaims, error) {
	return nil, nil
}

// trackingRefreshRepo records every rotation side effect (revoke/store) so a
// test can prove a failed role resolution never burns a rotation (R16).
type trackingRefreshRepo struct {
	record  *ports.RefreshTokenRecord
	revoked []struct{ id, replacedBy string }
	stored  []*ports.RefreshTokenRecord
}

func (r *trackingRefreshRepo) Store(ctx context.Context, token *ports.RefreshTokenRecord) error {
	r.stored = append(r.stored, token)
	return nil
}

func (r *trackingRefreshRepo) FindByHash(ctx context.Context, hash string) (*ports.RefreshTokenRecord, error) {
	if r.record == nil {
		return nil, domain.ErrNotFound
	}
	return r.record, nil
}

func (r *trackingRefreshRepo) Revoke(ctx context.Context, id, replacedBy string) error {
	r.revoked = append(r.revoked, struct{ id, replacedBy string }{id, replacedBy})
	return nil
}

func (r *trackingRefreshRepo) RevokeFamily(ctx context.Context, familyID string) error { return nil }

// newAuthTestService wires an authService with in-memory fakes. The fake
// hasher accepts any password so the tests focus on role resolution.
func newAuthTestService(users *fakeUserRepo, roles *fakeUserRoleRepo, tokens *recordingTokenManager, refresh *trackingRefreshRepo) ports.AuthService {
	if users == nil {
		users = &fakeUserRepo{}
	}
	if roles == nil {
		roles = &fakeUserRoleRepo{}
	}
	if tokens == nil {
		tokens = &recordingTokenManager{}
	}
	if refresh == nil {
		refresh = &trackingRefreshRepo{}
	}
	return NewAuthService(users, &fakeTenantRepo{}, tokens, &fakeHasher{ok: true}, roles, refresh, &recordingSink{}, time.Hour, time.Hour)
}

func activeUser() *domain.User {
	return &domain.User{ID: "user-1", TenantID: "tenant-1", IsActive: true, PasswordHash: "hash"}
}

// TestLoginEmbedsResolvedRole proves Login resolves the user's most-privileged
// role BEFORE issuing the token and embeds it in the claim (D3/R13).
func TestLoginEmbedsResolvedRole(t *testing.T) {
	users := &fakeUserRepo{user: activeUser()}
	roles := &fakeUserRoleRepo{roles: []*domain.UserRole{
		{UserID: "user-1", TenantID: "tenant-1", RoleCode: domain.RoleProducer},
		{UserID: "user-1", TenantID: "tenant-1", RoleCode: domain.RoleAgronomist},
	}}
	tokens := &recordingTokenManager{}
	svc := newAuthTestService(users, roles, tokens, nil)

	if _, err := svc.Login(context.Background(), "tenant-1", "a@b.test", "pass"); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if len(tokens.issued) != 1 {
		t.Fatalf("issued tokens = %d, want 1", len(tokens.issued))
	}
	if got := tokens.issued[0].Role; got != domain.RoleAgronomist {
		t.Fatalf("issued role claim = %q, want %q (most privileged)", got, domain.RoleAgronomist)
	}
}

// TestLoginRolelessEmbedsEmptyClaim proves a user with no memberships gets an
// empty role claim — roleless tokens are allowed, not an error.
func TestLoginRolelessEmbedsEmptyClaim(t *testing.T) {
	users := &fakeUserRepo{user: activeUser()}
	tokens := &recordingTokenManager{}
	svc := newAuthTestService(users, nil, tokens, nil)

	if _, err := svc.Login(context.Background(), "tenant-1", "a@b.test", "pass"); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if got := tokens.issued[0].Role; got != "" {
		t.Fatalf("issued role claim = %q, want empty", got)
	}
}

// TestLoginResolutionErrorFailsClosed proves a role-resolution failure aborts
// the login: no token is ever issued, so a failed resolution can never become
// a silently roleless token (fail-closed, D3).
func TestLoginResolutionErrorFailsClosed(t *testing.T) {
	users := &fakeUserRepo{user: activeUser()}
	roles := &fakeUserRoleRepo{listErr: errors.New("db down")}
	tokens := &recordingTokenManager{}
	svc := newAuthTestService(users, roles, tokens, nil)

	_, err := svc.Login(context.Background(), "tenant-1", "a@b.test", "pass")
	if err == nil {
		t.Fatal("Login must fail when role resolution fails (fail-closed)")
	}
	if len(tokens.issued) != 0 {
		t.Fatalf("issued tokens = %d, want 0 (never a roleless token)", len(tokens.issued))
	}
}

func validRefreshRecord() *ports.RefreshTokenRecord {
	return &ports.RefreshTokenRecord{
		ID: "rt-1", UserID: "user-1", TenantID: "tenant-1", FamilyID: "fam-1",
		TokenHash: "hash", ExpiresAt: time.Now().Add(time.Hour).Unix(),
	}
}

// TestRefreshPreservesResolvedRole proves Refresh re-resolves the role and
// carries it into the re-issued access token (R16: stateless tokens must not
// lose RBAC on rotation).
func TestRefreshPreservesResolvedRole(t *testing.T) {
	roles := &fakeUserRoleRepo{roles: []*domain.UserRole{
		{UserID: "user-1", TenantID: "tenant-1", RoleCode: domain.RoleProducer},
	}}
	refresh := &trackingRefreshRepo{record: validRefreshRecord()}
	tokens := &recordingTokenManager{}
	svc := newAuthTestService(nil, roles, tokens, refresh)

	if _, err := svc.Refresh(context.Background(), "plain-token"); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if len(tokens.issued) != 1 {
		t.Fatalf("issued tokens = %d, want 1", len(tokens.issued))
	}
	if got := tokens.issued[0].Role; got != domain.RoleProducer {
		t.Fatalf("refreshed role claim = %q, want %q", got, domain.RoleProducer)
	}
}

// TestRefreshResolutionErrorFailsClosedBeforeRotation proves resolution happens
// BEFORE the rotation side effects: on a resolution error the presented token
// is neither revoked nor replaced, so a failed resolution never burns a
// rotation (R16).
func TestRefreshResolutionErrorFailsClosedBeforeRotation(t *testing.T) {
	roles := &fakeUserRoleRepo{listErr: errors.New("db down")}
	refresh := &trackingRefreshRepo{record: validRefreshRecord()}
	svc := newAuthTestService(nil, roles, nil, refresh)

	_, err := svc.Refresh(context.Background(), "plain-token")
	if err == nil {
		t.Fatal("Refresh must fail when role resolution fails (fail-closed)")
	}
	if len(refresh.revoked) != 0 {
		t.Fatalf("revoked tokens = %d, want 0 (resolution must precede rotation)", len(refresh.revoked))
	}
	if len(refresh.stored) != 0 {
		t.Fatalf("stored tokens = %d, want 0 (resolution must precede rotation)", len(refresh.stored))
	}
}
