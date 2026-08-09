// Package services — emitter tests (PR3-T3 RED phase). They prove the auth and
// lot services emit breach events (login/refresh/lot.create) and that a replay
// emits the critical event with the request id. The constructors/signatures
// they use do not exist yet — that is the RED.
package services

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/ezequielranieri/agro-iam/internal/application/ports"
	"github.com/ezequielranieri/agro-iam/internal/domain"
)

// fakeAuditService records every Record call so tests can assert emission.
type fakeAuditService struct {
	records []auditCall
}

type auditCall struct {
	tenantID, actorID, action, severity string
}

func (f *fakeAuditService) Record(ctx context.Context, tenantID, actorUserID, action, entityType, entityID string,
	payload []byte, severity string) error {
	f.records = append(f.records, auditCall{tenantID: tenantID, actorID: actorUserID, action: action, severity: severity})
	return nil
}

func (f *fakeAuditService) VerifyChain(ctx context.Context, tenantID string) (int64, error) {
	return 0, nil
}

// bufLogger captures slog output so tests can assert critical events carry the
// request id.
func bufLogger(t *testing.T) (*slog.Logger, *strings.Builder) {
	t.Helper()
	var buf strings.Builder
	log := slog.New(slog.NewTextHandler(&buf, nil))
	return log, &buf
}

// fakeUserRepo implements ports.UserRepository.
type fakeUserRepo struct {
	user *domain.User
	err  error
}

func (f *fakeUserRepo) FindByEmail(ctx context.Context, tenantID, email string) (*domain.User, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.user == nil {
		return nil, domain.ErrNotFound
	}
	return f.user, nil
}

func (f *fakeUserRepo) FindByID(ctx context.Context, tenantID, id string) (*domain.User, error) {
	return nil, domain.ErrNotFound
}

func (f *fakeUserRepo) Create(ctx context.Context, user *domain.User) error { return nil }
func (f *fakeUserRepo) Update(ctx context.Context, user *domain.User) error { return nil }

// fakeTenantRepo implements ports.TenantRepository.
type fakeTenantRepo struct{}

func (f *fakeTenantRepo) FindByID(ctx context.Context, id string) (*domain.Tenant, error) {
	return &domain.Tenant{ID: id, Name: "Test"}, nil
}
func (f *fakeTenantRepo) Create(ctx context.Context, tenant *domain.Tenant) error { return nil }

// fakeTokenManager implements ports.TokenManager.
type fakeTokenManager struct{}

func (f *fakeTokenManager) Issue(claims ports.TokenClaims) (string, error) { return "access-token", nil }
func (f *fakeTokenManager) Verify(token string) (*ports.TokenClaims, error) {
	return &ports.TokenClaims{}, nil
}

// fakeHasher implements ports.PasswordHasher.
type fakeHasher struct {
	ok bool
}

func (f *fakeHasher) Hash(password string) (string, error) { return "hash", nil }
func (f *fakeHasher) Verify(encoded, password string) (bool, error) {
	return f.ok, nil
}

// fakeRefreshRepo implements ports.RefreshTokenRepository.
type fakeRefreshRepo struct {
	record *ports.RefreshTokenRecord
	store  []*ports.RefreshTokenRecord
}

func (f *fakeRefreshRepo) Store(ctx context.Context, token *ports.RefreshTokenRecord) error {
	f.store = append(f.store, token)
	return nil
}

func (f *fakeRefreshRepo) FindByHash(ctx context.Context, hash string) (*ports.RefreshTokenRecord, error) {
	if f.record == nil {
		return nil, domain.ErrNotFound
	}
	return f.record, nil
}

func (f *fakeRefreshRepo) Revoke(ctx context.Context, id, replacedBy string) error { return nil }
func (f *fakeRefreshRepo) RevokeFamily(ctx context.Context, familyID string) error { return nil }

// TestEmitterLoginEmitsAuthLogin proves a successful login emits
// auth.login info through the audit service.
func TestEmitterLoginEmitsAuthLogin(t *testing.T) {
	users := &fakeUserRepo{user: &domain.User{ID: "user-1", TenantID: "tenant-1", IsActive: true, PasswordHash: "hash"}}
	tokens := &fakeTokenManager{}
	refresh := &fakeRefreshRepo{}
	hasher := &fakeHasher{ok: true}

	audit := &fakeAuditService{}
	svc := NewAuthService(users, &fakeTenantRepo{}, tokens, hasher, refresh, audit, discardLogger(), time.Hour, time.Hour)

	_, err := svc.Login(context.Background(), "tenant-1", "a@b.test", "pass")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if len(audit.records) != 1 {
		t.Fatalf("audit records = %d, want 1 (auth.login)", len(audit.records))
	}
	rec := audit.records[0]
	if rec.action != "auth.login" || rec.severity != domain.SeverityInfo {
		t.Fatalf("record = %+v, want auth.login/info", rec)
	}
}

// TestEmitterRefreshEmitsAuthRefresh proves a successful refresh emits
// auth.refresh info.
func TestEmitterRefreshEmitsAuthRefresh(t *testing.T) {
	tokens := &fakeTokenManager{}
	refresh := &fakeRefreshRepo{record: &ports.RefreshTokenRecord{
		ID:        "rt-1",
		UserID:    "user-1",
		TenantID:  "tenant-1",
		FamilyID:  "fam-1",
		TokenHash: "hash",
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
	}}
	hasher := &fakeHasher{}

	audit := &fakeAuditService{}
	svc := NewAuthService(&fakeUserRepo{}, &fakeTenantRepo{}, tokens, hasher, refresh, audit, discardLogger(), time.Hour, time.Hour)

	_, err := svc.Refresh(context.Background(), "plain-token")
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if len(audit.records) != 1 {
		t.Fatalf("audit records = %d, want 1 (auth.refresh)", len(audit.records))
	}
	rec := audit.records[0]
	if rec.action != "auth.refresh" || rec.severity != domain.SeverityInfo {
		t.Fatalf("record = %+v, want auth.refresh/info", rec)
	}
}

// TestEmitterReplayEmitsCriticalWithRequestID proves a replay (revoked with a
// replacement) emits auth.refresh.replay critical — and the slog line carries
// the request id.
func TestEmitterReplayEmitsCriticalWithRequestID(t *testing.T) {
	tokens := &fakeTokenManager{}
	replacedAt := time.Now().Add(-time.Minute).Unix()
	refresh := &fakeRefreshRepo{record: &ports.RefreshTokenRecord{
		ID:         "rt-old",
		UserID:     "user-1",
		TenantID:   "tenant-1",
		FamilyID:   "fam-1",
		TokenHash:  "hash",
		ExpiresAt:  time.Now().Add(time.Hour).Unix(),
		RevokedAt:  &replacedAt,
		ReplacedBy: "rt-next",
	}}
	hasher := &fakeHasher{}

	audit := &fakeAuditService{}
	log, buf := bufLogger(t)
	svc := NewAuthService(&fakeUserRepo{}, &fakeTenantRepo{}, tokens, hasher, refresh, audit, log, time.Hour, time.Hour)

	_, err := svc.Refresh(context.Background(), "plain-token")
	if !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("Refresh error = %v, want ErrUnauthorized", err)
	}

	// Audit row: critical replay event for the tenant.
	if len(audit.records) != 1 {
		t.Fatalf("audit records = %d, want 1 (replay critical)", len(audit.records))
	}
	rec := audit.records[0]
	if rec.action != "auth.refresh.replay" || rec.severity != domain.SeverityCritical {
		t.Fatalf("record = %+v, want auth.refresh.replay/critical", rec)
	}

	// Log line must carry a request id.
	logOut := buf.String()
	if !strings.Contains(logOut, "request_id") {
		t.Fatalf("log output missing request_id:\n%s", logOut)
	}
}

// TestEmitterLotCreateEmitsLotCreate proves lot creation emits lot.create info
// with the actor user id.
func TestEmitterLotCreateEmitsLotCreate(t *testing.T) {
	repo := &fakeLotRepo{}
	audit := &fakeAuditService{}
	svc := NewLotService(repo, audit, discardLogger())

	lot, err := svc.Create(context.Background(), "tenant-1", "actor-user-1", "Campo Norte", 12.5, "soy")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if lot == nil {
		t.Fatal("lot must not be nil")
	}

	if len(audit.records) != 1 {
		t.Fatalf("audit records = %d, want 1 (lot.create)", len(audit.records))
	}
	rec := audit.records[0]
	if rec.action != "lot.create" || rec.severity != domain.SeverityInfo {
		t.Fatalf("record = %+v, want lot.create/info", rec)
	}
	if rec.actorID != "actor-user-1" {
		t.Fatalf("actorID = %q, want actor-user-1 (Create must receive the actor)", rec.actorID)
	}
}
