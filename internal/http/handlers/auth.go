package handlers

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/ezequielranieri/agro-iam/internal/application/ports"
	"github.com/ezequielranieri/agro-iam/internal/domain"
)

// AuthHandler exposes the login and refresh endpoints. It knows only
// ports.AuthService â€” no JWT or Argon2id types leak into the HTTP layer.
type AuthHandler struct {
	auth ports.AuthService
	log  *slog.Logger
}

// NewAuthHandler wires the handler.
func NewAuthHandler(auth ports.AuthService, log *slog.Logger) *AuthHandler {
	return &AuthHandler{auth: auth, log: log}
}

// loginRequest is the body of POST /api/v1/auth/login.
//
// DEV NOTE / DEVIATION: the original spec fixed the body as {"email","password"},
// but the schema FORCEs RLS on app.users and login must look the user up inside
// its own tenant â€” you cannot SELECT a user before you know the tenant, and the
// tenant cannot be discovered FROM the user without breaking isolation. A
// tenant identifier is therefore required at login (the same pattern Auth0 /
// Cognito use with realms). tenant_id is read here and threaded into the
// tenant-scoped transaction.
type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	TenantID string `json:"tenant_id"`
}

type loginResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	session, err := h.auth.Login(r.Context(), req.TenantID, req.Email, req.Password)
	if err != nil {
		// Unauthorized must not leak whether email, password or tenant failed.
		h.log.Info("login failed", "email", req.Email, "error", err.Error())
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	writeJSON(w, http.StatusOK, loginResponse{
		AccessToken:  session.AccessToken,
		RefreshToken: session.RefreshToken,
		ExpiresIn:    session.ExpiresIn,
	})
}

// refreshRequest is the body of POST /api/v1/auth/refresh.
type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

func (h *AuthHandler) Refresh(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	session, err := h.auth.Refresh(r.Context(), req.RefreshToken)
	if errors.Is(err, domain.ErrUnauthorized) {
		// A replay or expired token collapses to the same 401.
		writeError(w, http.StatusUnauthorized, "invalid or expired refresh token")
		return
	}
	if err != nil {
		h.log.Error("refresh failed", "error", err.Error())
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	writeJSON(w, http.StatusOK, loginResponse{
		AccessToken:  session.AccessToken,
		RefreshToken: session.RefreshToken,
		ExpiresIn:    session.ExpiresIn,
	})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
