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

// ApplicationsHandler exposes the tenant-scoped application endpoints. The
// tenant id is read from the request context — injected by the RequireAuth
// middleware — and never from the client. Write routes are RequireAuth-protected
// in this PR; the role guard (admin | agronomist | producer) lands with
// RequireRole in PR D2 (R8).
type ApplicationsHandler struct {
	applications ports.ApplicationService
	log          *slog.Logger
}

// NewApplicationsHandler wires the handler.
func NewApplicationsHandler(applications ports.ApplicationService, log *slog.Logger) *ApplicationsHandler {
	return &ApplicationsHandler{applications: applications, log: log}
}

// applicationResponse is the stable JSON shape of an application. OperatorID
// is nullable: an empty operator renders as JSON null.
type applicationResponse struct {
	ID          string  `json:"id"`
	LotID       string  `json:"lot_id"`
	CampaignID  string  `json:"campaign_id"`
	ProductName string  `json:"product_name"`
	Dose        string  `json:"dose"`
	AppliedAt   string  `json:"applied_at"`
	OperatorID  *string `json:"operator_id"`
	Notes       string  `json:"notes"`
	CreatedAt   string  `json:"created_at"`
}

func toApplicationResponse(a *domain.Application) applicationResponse {
	return applicationResponse{
		ID:          a.ID,
		LotID:       a.LotID,
		CampaignID:  a.CampaignID,
		ProductName: a.ProductName,
		Dose:        a.Dose,
		AppliedAt:   a.AppliedAt.UTC().Format(time.RFC3339),
		OperatorID:  nullableStringPtr(a.OperatorID),
		Notes:       a.Notes,
		CreatedAt:   a.CreatedAt.UTC().Format(time.RFC3339),
	}
}

func nullableStringPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// List returns every application of the authenticated tenant.
func (h *ApplicationsHandler) List(w http.ResponseWriter, r *http.Request) {
	tenantID := claims.TenantIDFrom(r.Context())
	if tenantID == "" {
		// Reached without the middleware's claims: the request was never
		// authenticated, so 401 rather than 500.
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	applications, err := h.applications.List(r.Context(), tenantID)
	if err != nil {
		writeApplicationError(w, h.log, "list applications", tenantID, err)
		return
	}

	resp := make([]applicationResponse, 0, len(applications))
	for _, a := range applications {
		resp = append(resp, toApplicationResponse(a))
	}
	writeJSON(w, http.StatusOK, map[string]any{"applications": resp})
}

// applicationRequest is the body of POST /api/v1/applications and PATCH
// /api/v1/applications/{id}. AppliedAt is an optional RFC3339 timestamp (zero
// defaulted by the service clock); an empty operator_id maps to NULL.
type applicationRequest struct {
	LotID       string     `json:"lot_id"`
	CampaignID  string     `json:"campaign_id"`
	ProductName string     `json:"product_name"`
	Dose        string     `json:"dose"`
	AppliedAt   *time.Time `json:"applied_at"`
	OperatorID  string     `json:"operator_id"`
	Notes       string     `json:"notes"`
}

// GetByID returns one application of the authenticated tenant.
func (h *ApplicationsHandler) GetByID(w http.ResponseWriter, r *http.Request) {
	tenantID := claims.TenantIDFrom(r.Context())
	if tenantID == "" {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	application, err := h.applications.GetByID(r.Context(), tenantID, r.PathValue("id"))
	if err != nil {
		writeApplicationError(w, h.log, "get application", tenantID, err)
		return
	}
	writeJSON(w, http.StatusOK, toApplicationResponse(application))
}

// Create registers a new application owned by the authenticated tenant.
func (h *ApplicationsHandler) Create(w http.ResponseWriter, r *http.Request) {
	tenantID := claims.TenantIDFrom(r.Context())
	if tenantID == "" {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req applicationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	application, err := h.applications.Create(r.Context(), tenantID, claims.UserIDFrom(r.Context()), ports.ApplicationInput{
		LotID:       req.LotID,
		CampaignID:  req.CampaignID,
		ProductName: req.ProductName,
		Dose:        req.Dose,
		AppliedAt:   derefTime(req.AppliedAt),
		OperatorID:  req.OperatorID,
		Notes:       req.Notes,
	})
	if err != nil {
		writeApplicationError(w, h.log, "create application", tenantID, err)
		return
	}
	writeJSON(w, http.StatusCreated, toApplicationResponse(application))
}

// Update replaces the mutable fields of an existing application (full-row
// replace, no partial PATCH semantics).
func (h *ApplicationsHandler) Update(w http.ResponseWriter, r *http.Request) {
	tenantID := claims.TenantIDFrom(r.Context())
	if tenantID == "" {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req applicationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	application, err := h.applications.Update(r.Context(), tenantID, claims.UserIDFrom(r.Context()), r.PathValue("id"), ports.ApplicationInput{
		LotID:       req.LotID,
		CampaignID:  req.CampaignID,
		ProductName: req.ProductName,
		Dose:        req.Dose,
		AppliedAt:   derefTime(req.AppliedAt),
		OperatorID:  req.OperatorID,
		Notes:       req.Notes,
	})
	if err != nil {
		writeApplicationError(w, h.log, "update application", tenantID, err)
		return
	}
	writeJSON(w, http.StatusOK, toApplicationResponse(application))
}

// Delete removes an application owned by the authenticated tenant.
func (h *ApplicationsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	tenantID := claims.TenantIDFrom(r.Context())
	if tenantID == "" {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	if err := h.applications.Delete(r.Context(), tenantID, claims.UserIDFrom(r.Context()), r.PathValue("id")); err != nil {
		writeApplicationError(w, h.log, "delete application", tenantID, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func derefTime(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

// writeApplicationError maps service errors onto the HTTP contract: invalid
// input 400, missing row 404, conflict 409, forbidden 403, tenant required 401;
// any other error is logged and collapsed to a 500.
func writeApplicationError(w http.ResponseWriter, log *slog.Logger, op, tenantID string, err error) {
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
