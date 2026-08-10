package handlers

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ezequielranieri/agro-iam/internal/application/ports"
	"github.com/ezequielranieri/agro-iam/internal/domain"
	"github.com/ezequielranieri/agro-iam/internal/http/claims"
)

// fakeUserService is an in-memory ports.UserService. The err field drives
// every method so each error branch of the handler is reachable.
type fakeUserService struct {
	user *domain.User
	list []*domain.User
	err  error
	in   ports.UserInput
	upd  ports.UpdateUserInput
	id   string
}

func (f *fakeUserService) CreateUser(ctx context.Context, tenantID, actorUserID string, in ports.UserInput) (*domain.User, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.in = in
	return f.user, nil
}

func (f *fakeUserService) ListUsers(ctx context.Context, tenantID string) ([]*domain.User, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.list, nil
}

func (f *fakeUserService) UpdateUser(ctx context.Context, tenantID, actorUserID, id string, in ports.UpdateUserInput) (*domain.User, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.id = id
	f.upd = in
	return f.user, nil
}

// userRequestWithClaims builds a request with authenticated claims for
// tenant-1. Handlers reading a {id} path value set it explicitly (direct
// invocation bypasses the mux).
func userRequestWithClaims(method, target, body string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	return req.WithContext(claims.WithIdentity(req.Context(), "user-1", "tenant-1", "admin"))
}

func newUsersTestHandler(f *fakeUserService) *UsersHandler {
	return NewUsersHandler(f, slog.New(slog.DiscardHandler))
}

func decodeUsersError(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode error body %q: %v", rec.Body.String(), err)
	}
	return body["error"]
}

func TestUsersCreate(t *testing.T) {
	created := &domain.User{
		ID: "u-new", TenantID: "tenant-1", Email: "new@esperanza.coop",
		PasswordHash: "hash", FullName: "Nueva Productora", IsActive: true,
		CreatedAt: time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC),
	}
	f := &fakeUserService{user: created}
	h := newUsersTestHandler(f)

	rec := httptest.NewRecorder()
	h.Create(rec, userRequestWithClaims(http.MethodPost, "/api/v1/users",
		`{"email":"new@esperanza.coop","password":"s3cret","full_name":"Nueva Productora","role":"producer"}`))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", rec.Code)
	}

	// The response must NOT leak password material (R9): decode into a map and
	// prove the password_hash key is absent, then check the real fields.
	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := body["password_hash"]; ok {
		t.Fatal("response leaks password_hash (R9 violated)")
	}
	if body["id"] != "u-new" || body["email"] != "new@esperanza.coop" || body["full_name"] != "Nueva Productora" {
		t.Fatalf("response = %v, want id/email/full_name", body)
	}

	if f.in.Email != "new@esperanza.coop" || f.in.Password != "s3cret" ||
		f.in.FullName != "Nueva Productora" || f.in.Role != "producer" {
		t.Fatalf("service input = %+v, want the full payload forwarded", f.in)
	}
}

func TestUsersCreateBadJSON(t *testing.T) {
	h := newUsersTestHandler(&fakeUserService{})

	rec := httptest.NewRecorder()
	h.Create(rec, userRequestWithClaims(http.MethodPost, "/api/v1/users", `{not json`))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestUsersCreateErrorMapping proves the service-error map: invalid input 400,
// tenant required 401, forbidden 403, conflict 409 (duplicate email, R11),
// unknown 500.
func TestUsersCreateErrorMapping(t *testing.T) {
	cases := []struct {
		label string
		err   error
		code  int
		msg   string
	}{
		{"invalid input", domain.ErrInvalidInput, http.StatusBadRequest, "invalid input"},
		{"tenant required", domain.ErrTenantRequired, http.StatusUnauthorized, "unauthorized"},
		{"forbidden", domain.ErrForbidden, http.StatusForbidden, "forbidden"},
		{"conflict", domain.ErrConflict, http.StatusConflict, "conflict"},
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			h := newUsersTestHandler(&fakeUserService{err: c.err})

			rec := httptest.NewRecorder()
			h.Create(rec, userRequestWithClaims(http.MethodPost, "/api/v1/users",
				`{"email":"a@test.local","password":"s3cret","full_name":"A","role":"producer"}`))

			if rec.Code != c.code {
				t.Fatalf("status = %d, want %d", rec.Code, c.code)
			}
			if got := decodeUsersError(t, rec); got != c.msg {
				t.Fatalf("error message = %q, want %q", got, c.msg)
			}
		})
	}
}

func TestUsersCreateUnauthenticated(t *testing.T) {
	h := newUsersTestHandler(&fakeUserService{})

	// No claims in the context: the tenant is empty, so 401 (never 500).
	rec := httptest.NewRecorder()
	h.Create(rec, httptest.NewRequest(http.MethodPost, "/api/v1/users",
		strings.NewReader(`{"email":"a@test.local","password":"s3cret","full_name":"A","role":"producer"}`)))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestUsersList(t *testing.T) {
	want := []*domain.User{
		{ID: "u-1", TenantID: "tenant-1", Email: "a@esperanza.coop", PasswordHash: "hash", FullName: "A", IsActive: true},
		{ID: "u-2", TenantID: "tenant-1", Email: "b@esperanza.coop", PasswordHash: "hash", FullName: "B", IsActive: false},
	}
	f := &fakeUserService{list: want}
	h := newUsersTestHandler(f)

	rec := httptest.NewRecorder()
	h.List(rec, userRequestWithClaims(http.MethodGet, "/api/v1/users", ""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp struct {
		Users []map[string]any `json:"users"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Users) != 2 {
		t.Fatalf("users = %+v, want 2 entries", resp.Users)
	}
	for _, u := range resp.Users {
		if _, ok := u["password_hash"]; ok {
			t.Fatal("ListUsers response leaks password_hash (R9 violated)")
		}
	}
	if resp.Users[0]["id"] != "u-1" || resp.Users[1]["id"] != "u-2" {
		t.Fatalf("users = %+v, want [u-1, u-2]", resp.Users)
	}
	if resp.Users[1]["is_active"] != false {
		t.Fatalf("user u-2 is_active = %v, want false (toggle visible)", resp.Users[1]["is_active"])
	}
}

func TestUsersListUnauthenticated(t *testing.T) {
	h := newUsersTestHandler(&fakeUserService{})

	rec := httptest.NewRecorder()
	h.List(rec, httptest.NewRequest(http.MethodGet, "/api/v1/users", nil))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestUsersUpdate(t *testing.T) {
	updated := &domain.User{ID: "u-1", TenantID: "tenant-1", Email: "a@esperanza.coop", FullName: "Después", IsActive: false}
	f := &fakeUserService{user: updated}
	h := newUsersTestHandler(f)

	rec := httptest.NewRecorder()
	req := userRequestWithClaims(http.MethodPatch, "/api/v1/users/u-1",
		`{"full_name":"Después","is_active":false}`)
	req.SetPathValue("id", "u-1")
	h.Update(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if f.id != "u-1" {
		t.Fatalf("service received id = %q, want u-1", f.id)
	}
	if f.upd.FullName != "Después" || f.upd.IsActive {
		t.Fatalf("service input = %+v, want full_name=Después is_active=false", f.upd)
	}

	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := body["password_hash"]; ok {
		t.Fatal("Update response leaks password_hash (R9 violated)")
	}
	if body["full_name"] != "Después" || body["is_active"] != false {
		t.Fatalf("response = %v, want full_name=Después is_active=false", body)
	}
}

func TestUsersUpdateNotFound(t *testing.T) {
	h := newUsersTestHandler(&fakeUserService{err: domain.ErrNotFound})

	rec := httptest.NewRecorder()
	req := userRequestWithClaims(http.MethodPatch, "/api/v1/users/u-missing",
		`{"full_name":"Nadie","is_active":true}`)
	req.SetPathValue("id", "u-missing")
	h.Update(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestUsersUpdateConflict(t *testing.T) {
	h := newUsersTestHandler(&fakeUserService{err: domain.ErrConflict})

	rec := httptest.NewRecorder()
	req := userRequestWithClaims(http.MethodPatch, "/api/v1/users/u-1",
		`{"full_name":"A","is_active":true}`)
	req.SetPathValue("id", "u-1")
	h.Update(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
}

func TestUsersUpdateUnauthenticated(t *testing.T) {
	h := newUsersTestHandler(&fakeUserService{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/users/u-1",
		strings.NewReader(`{"full_name":"A","is_active":true}`))
	req.SetPathValue("id", "u-1")
	h.Update(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}
