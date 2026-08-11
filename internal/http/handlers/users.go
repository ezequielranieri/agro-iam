package handlers

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/ezequielranieri/agro-iam/internal/application/ports"
	"github.com/ezequielranieri/agro-iam/internal/domain"
	"github.com/ezequielranieri/agro-iam/internal/http/claims"
)

// UsersHandler exposes the tenant-scoped provisioning endpoints. The tenant id
// is read from the request context — injected by the RequireAuth middleware —
// and never from the client, so a forged payload cannot cross tenants. The
// routes are RequireAuth-protected in this PR; the admin-only guard (R12)
// lands with the RequireRole middleware in PR D2.
type UsersHandler struct {
	users ports.UserService
	log   *slog.Logger
}

// NewUsersHandler wires the handler.
func NewUsersHandler(users ports.UserService, log *slog.Logger) *UsersHandler {
	return &UsersHandler{users: users, log: log}
}

// userResponse is the stable JSON shape of a user. PasswordHash is
// deliberately absent: provisioning responses never carry password material
// (R9). Role is the server-resolved membership (S1.8) — never a client claim.
type userResponse struct {
	ID        string `json:"id"`
	Email     string `json:"email"`
	FullName  string `json:"full_name"`
	IsActive  bool   `json:"is_active"`
	Role      string `json:"role"`
	CreatedAt string `json:"created_at"`
}

func toUserResponse(u *domain.User) userResponse {
	return userResponse{
		ID:        u.ID,
		Email:     u.Email,
		FullName:  u.FullName,
		IsActive:  u.IsActive,
		Role:      u.Role,
		CreatedAt: u.CreatedAt.UTC().Format(time.RFC3339),
	}
}

// userRequest is the body of POST /api/v1/users. Password is the plaintext to
// hash server-side; it is never stored or returned as-is.
type userRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	FullName string `json:"full_name"`
	Role     string `json:"role"`
}

// updateUserRequest is the body of PATCH /api/v1/users/{id}: a full-row
// replace of the mutable fields (no partial PATCH semantics, per the slice 4
// design decision).
type updateUserRequest struct {
	FullName string `json:"full_name"`
	IsActive bool   `json:"is_active"`
}

// List returns every user of the authenticated tenant.
func (h *UsersHandler) List(w http.ResponseWriter, r *http.Request) {
	tenantID := claims.TenantIDFrom(r.Context())
	if tenantID == "" {
		// Reached without the middleware's claims: the request was never
		// authenticated, so 401 rather than 500.
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	users, err := h.users.ListUsers(r.Context(), tenantID)
	if err != nil {
		writeUserError(w, h.log, "list users", tenantID, err)
		return
	}

	resp := make([]userResponse, 0, len(users))
	for _, u := range users {
		resp = append(resp, toUserResponse(u))
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": resp})
}

// Create provisions a new user in the authenticated tenant (admin-only guard
// lands in D2). The plaintext password never appears in the response.
func (h *UsersHandler) Create(w http.ResponseWriter, r *http.Request) {
	tenantID := claims.TenantIDFrom(r.Context())
	if tenantID == "" {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req userRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	user, err := h.users.CreateUser(r.Context(), tenantID, claims.UserIDFrom(r.Context()), ports.UserInput{
		Email:    req.Email,
		Password: req.Password,
		FullName: req.FullName,
		Role:     req.Role,
	})
	if err != nil {
		writeUserError(w, h.log, "create user", tenantID, err)
		return
	}
	writeJSON(w, http.StatusCreated, toUserResponse(user))
}

// Update replaces the mutable fields of an existing user (full_name, is_active
// toggle; full-row replace, no partial PATCH semantics).
func (h *UsersHandler) Update(w http.ResponseWriter, r *http.Request) {
	tenantID := claims.TenantIDFrom(r.Context())
	if tenantID == "" {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req updateUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	user, err := h.users.UpdateUser(r.Context(), tenantID, claims.UserIDFrom(r.Context()), r.PathValue("id"), ports.UpdateUserInput{
		FullName: req.FullName,
		IsActive: req.IsActive,
	})
	if err != nil {
		writeUserError(w, h.log, "update user", tenantID, err)
		return
	}
	writeJSON(w, http.StatusOK, toUserResponse(user))
}

// writeUserError maps service errors onto the HTTP contract: invalid input
// 400, missing row 404, conflict 409 (duplicate email, R11), forbidden 403,
// tenant required 401; any other error is logged and collapsed to a 500.
func writeUserError(w http.ResponseWriter, log *slog.Logger, op, tenantID string, err error) {
	switch {
	case errors.Is(err, domain.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "invalid input")
	case errors.Is(err, domain.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, domain.ErrConflict):
		writeError(w, http.StatusConflict, "conflict")
	case errors.Is(err, domain.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden")
	case errors.Is(err, domain.ErrTenantRequired):
		writeError(w, http.StatusUnauthorized, "unauthorized")
	default:
		log.Error(op, "tenant_id", tenantID, "error", err.Error())
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}
