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

// CampaignsHandler exposes the tenant-scoped campaign endpoints. The tenant id
// is read from the request context — injected by the RequireAuth middleware —
// and never from the client, so a forged payload cannot cross tenants. Write
// routes are RequireAuth-protected in this PR; the role guard (admin |
// agronomist) lands with the RequireRole middleware in PR D2.
type CampaignsHandler struct {
	campaigns ports.CampaignService
	log       *slog.Logger
}

// NewCampaignsHandler wires the handler.
func NewCampaignsHandler(campaigns ports.CampaignService, log *slog.Logger) *CampaignsHandler {
	return &CampaignsHandler{campaigns: campaigns, log: log}
}

// campaignResponse is the stable JSON shape of a campaign. A dedicated
// response struct keeps the wire format explicit instead of leaking raw domain
// timestamps; the date pointers are null when the campaign has none.
type campaignResponse struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Season    string  `json:"season"`
	StartedAt *string `json:"started_at"`
	EndedAt   *string `json:"ended_at"`
	CreatedAt string  `json:"created_at"`
}

func toCampaignResponse(c *domain.Campaign) campaignResponse {
	return campaignResponse{
		ID:        c.ID,
		Name:      c.Name,
		Season:    c.Season,
		StartedAt: formatTimePtr(c.StartedAt),
		EndedAt:   formatTimePtr(c.EndedAt),
		CreatedAt: c.CreatedAt.UTC().Format(time.RFC3339),
	}
}

func formatTimePtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}

// List returns every campaign of the authenticated tenant.
func (h *CampaignsHandler) List(w http.ResponseWriter, r *http.Request) {
	tenantID := claims.TenantIDFrom(r.Context())
	if tenantID == "" {
		// Reached without the middleware's claims: the request was never
		// authenticated, so 401 rather than 500.
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	campaigns, err := h.campaigns.List(r.Context(), tenantID)
	if err != nil {
		writeCampaignError(w, h.log, "list campaigns", tenantID, err)
		return
	}

	resp := make([]campaignResponse, 0, len(campaigns))
	for _, c := range campaigns {
		resp = append(resp, toCampaignResponse(c))
	}
	writeJSON(w, http.StatusOK, map[string]any{"campaigns": resp})
}

// campaignRequest is the body of POST /api/v1/campaigns and PATCH
// /api/v1/campaigns/{id}. Dates are optional RFC3339 timestamps.
type campaignRequest struct {
	Name      string     `json:"name"`
	Season    string     `json:"season"`
	StartedAt *time.Time `json:"started_at"`
	EndedAt   *time.Time `json:"ended_at"`
}

// GetByID returns one campaign of the authenticated tenant.
func (h *CampaignsHandler) GetByID(w http.ResponseWriter, r *http.Request) {
	tenantID := claims.TenantIDFrom(r.Context())
	if tenantID == "" {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	campaign, err := h.campaigns.GetByID(r.Context(), tenantID, r.PathValue("id"))
	if err != nil {
		writeCampaignError(w, h.log, "get campaign", tenantID, err)
		return
	}
	writeJSON(w, http.StatusOK, toCampaignResponse(campaign))
}

// Create registers a new campaign owned by the authenticated tenant.
func (h *CampaignsHandler) Create(w http.ResponseWriter, r *http.Request) {
	tenantID := claims.TenantIDFrom(r.Context())
	if tenantID == "" {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req campaignRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	campaign, err := h.campaigns.Create(r.Context(), tenantID, claims.UserIDFrom(r.Context()), ports.CampaignInput{
		Name:      req.Name,
		Season:    req.Season,
		StartedAt: req.StartedAt,
		EndedAt:   req.EndedAt,
	})
	if err != nil {
		writeCampaignError(w, h.log, "create campaign", tenantID, err)
		return
	}
	writeJSON(w, http.StatusCreated, toCampaignResponse(campaign))
}

// Update replaces the mutable fields of an existing campaign (full-row
// replace, no partial PATCH semantics).
func (h *CampaignsHandler) Update(w http.ResponseWriter, r *http.Request) {
	tenantID := claims.TenantIDFrom(r.Context())
	if tenantID == "" {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req campaignRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	campaign, err := h.campaigns.Update(r.Context(), tenantID, claims.UserIDFrom(r.Context()), r.PathValue("id"), ports.CampaignInput{
		Name:      req.Name,
		Season:    req.Season,
		StartedAt: req.StartedAt,
		EndedAt:   req.EndedAt,
	})
	if err != nil {
		writeCampaignError(w, h.log, "update campaign", tenantID, err)
		return
	}
	writeJSON(w, http.StatusOK, toCampaignResponse(campaign))
}

// Delete removes a campaign owned by the authenticated tenant.
func (h *CampaignsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	tenantID := claims.TenantIDFrom(r.Context())
	if tenantID == "" {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	if err := h.campaigns.Delete(r.Context(), tenantID, claims.UserIDFrom(r.Context()), r.PathValue("id")); err != nil {
		writeCampaignError(w, h.log, "delete campaign", tenantID, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeCampaignError maps service errors onto the HTTP contract: invalid input
// 400, missing row 404, conflict 409, forbidden 403, tenant required 401; any
// other error is logged and collapsed to a 500.
func writeCampaignError(w http.ResponseWriter, log *slog.Logger, op, tenantID string, err error) {
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
