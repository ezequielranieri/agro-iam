// Package services — emitter tests (PR2-T1 RED phase). They prove the auth and
// lot services emit breach signals through the sink: login/refresh/lot.create
// info events and the critical replay event carrying the request id. The
// constructors/signatures they use do not exist yet — that is the RED.
package services

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ezequielranieri/agro-iam/internal/application/ports"
	"github.com/ezequielranieri/agro-iam/internal/domain"
	"github.com/ezequielranieri/agro-iam/internal/requestid"
)

// fakeUserRepo implements ports.UserRepository. The err field drives every
// method so each error branch of the service is reachable; created/updated
// record the writes the service performed.
type fakeUserRepo struct {
	user    *domain.User
	list    []*domain.User
	err     error
	created []*domain.User
	updated []*domain.User
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
	if f.err != nil {
		return nil, f.err
	}
	if f.user == nil {
		return nil, domain.ErrNotFound
	}
	return f.user, nil
}

func (f *fakeUserRepo) List(ctx context.Context, tenantID string) ([]*domain.User, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.list, nil
}

func (f *fakeUserRepo) Create(ctx context.Context, user *domain.User) error {
	if f.err != nil {
		return f.err
	}
	f.created = append(f.created, user)
	return nil
}

func (f *fakeUserRepo) Update(ctx context.Context, user *domain.User) error {
	if f.err != nil {
		return f.err
	}
	f.updated = append(f.updated, user)
	return nil
}

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

// TestEmitterLoginEmitsAuthLogin proves a successful login emits a
// login_success/auth.login info signal with the tenant and actor through the
// sink.
func TestEmitterLoginEmitsAuthLogin(t *testing.T) {
	users := &fakeUserRepo{user: &domain.User{ID: "user-1", TenantID: "tenant-1", IsActive: true, PasswordHash: "hash"}}
	tokens := &fakeTokenManager{}
	refresh := &fakeRefreshRepo{}
	hasher := &fakeHasher{ok: true}

	sink := &recordingSink{}
	svc := NewAuthService(users, &fakeTenantRepo{}, tokens, hasher, refresh, sink, time.Hour, time.Hour)

	_, err := svc.Login(context.Background(), "tenant-1", "a@b.test", "pass")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if len(sink.events) != 1 {
		t.Fatalf("emitted signals = %d, want 1 (auth.login)", len(sink.events))
	}
	ev := sink.events[0]
	if ev.Signal != string(SignalLoginSuccess) || ev.Action != "auth.login" || ev.Severity != domain.SeverityInfo {
		t.Fatalf("event = %+v, want login_success/auth.login/info", ev)
	}
	if ev.TenantID != "tenant-1" || ev.ActorID != "user-1" {
		t.Fatalf("event identity = %+v, want tenant-1/user-1", ev)
	}
}

// TestEmitterRefreshEmitsAuthRefresh proves a successful refresh emits a
// refresh_success/auth.refresh info signal.
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

	sink := &recordingSink{}
	svc := NewAuthService(&fakeUserRepo{}, &fakeTenantRepo{}, tokens, hasher, refresh, sink, time.Hour, time.Hour)

	_, err := svc.Refresh(context.Background(), "plain-token")
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if len(sink.events) != 1 {
		t.Fatalf("emitted signals = %d, want 1 (auth.refresh)", len(sink.events))
	}
	ev := sink.events[0]
	if ev.Signal != string(SignalRefreshSuccess) || ev.Action != "auth.refresh" || ev.Severity != domain.SeverityInfo {
		t.Fatalf("event = %+v, want refresh_success/auth.refresh/info", ev)
	}
}

// TestEmitterReplayEmitsCriticalWithRequestID proves a replay (revoked with a
// replacement) emits a refresh_replay/auth.refresh.replay critical signal and
// the sink event carries the request id from the context.
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

	sink := &recordingSink{}
	svc := NewAuthService(&fakeUserRepo{}, &fakeTenantRepo{}, tokens, hasher, refresh, sink, time.Hour, time.Hour)

	ctx := requestid.WithRequestID(context.Background(), "req-42")
	_, err := svc.Refresh(ctx, "plain-token")
	if !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("Refresh error = %v, want ErrUnauthorized", err)
	}

	if len(sink.events) != 1 {
		t.Fatalf("emitted signals = %d, want 1 (replay critical)", len(sink.events))
	}
	ev := sink.events[0]
	if ev.Signal != string(SignalRefreshReplay) || ev.Action != "auth.refresh.replay" || ev.Severity != domain.SeverityCritical {
		t.Fatalf("event = %+v, want refresh_replay/auth.refresh.replay/critical", ev)
	}
	if ev.RequestID != "req-42" {
		t.Fatalf("event request_id = %q, want req-42 (correlated from context)", ev.RequestID)
	}
}

// TestEmitterLotCreateEmitsLotCreate proves lot creation emits a lot.create
// info signal with the actor user id and an empty Signal (R6: no new signal).
func TestEmitterLotCreateEmitsLotCreate(t *testing.T) {
	repo := &fakeLotRepo{}
	sink := &recordingSink{}
	svc := NewLotService(repo, sink)

	lot, err := svc.Create(context.Background(), "tenant-1", "actor-user-1", "Campo Norte", 12.5, "soy")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if lot == nil {
		t.Fatal("lot must not be nil")
	}

	if len(sink.events) != 1 {
		t.Fatalf("emitted signals = %d, want 1 (lot.create)", len(sink.events))
	}
	ev := sink.events[0]
	if ev.Signal != "" || ev.Action != "lot.create" || ev.Severity != domain.SeverityInfo {
		t.Fatalf("event = %+v, want Signal=\"\" lot.create/info", ev)
	}
	if ev.ActorID != "actor-user-1" {
		t.Fatalf("actorID = %q, want actor-user-1 (Create must receive the actor)", ev.ActorID)
	}
}
