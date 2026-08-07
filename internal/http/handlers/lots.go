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

// LotsHandler exposes the tenant-scoped lot endpoints. The tenant id is read
// from the request context â€” injected by the RequireAuth middleware â€” and never
// from the client, so a forged payload cannot cross tenants.
type LotsHandler struct {
	lots ports.LotService
	log  *slog.Logger
}

// NewLotsHandler wires the handler.
func NewLotsHandler(lots ports.LotService, log *slog.Logger) *LotsHandler {
	return &LotsHandler{lots: lots, log: log}
}

// lotResponse is the stable JSON shape of a lot. A dedicated response struct
// keeps the wire format explicit instead of leaking raw domain timestamps.
type lotResponse struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	AreaHA    float64 `json:"area_ha"`
	Crop      string  `json:"crop"`
	CreatedAt string  `json:"created_at"`
}

func toLotResponse(l *domain.Lot) lotResponse {
	return lotResponse{
		ID:        l.ID,
		Name:      l.Name,
		AreaHA:    l.AreaHA,
		Crop:      l.Crop,
		CreatedAt: l.CreatedAt.UTC().Format(time.RFC3339),
	}
}

// List returns every lot of the authenticated tenant.
func (h *LotsHandler) List(w http.ResponseWriter, r *http.Request) {
	tenantID := claims.TenantIDFrom(r.Context())
	if tenantID == "" {
		// Reached without the middleware's claims: the request was never
		// authenticated, so 401 rather than 500.
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	lots, err := h.lots.ListByTenant(r.Context(), tenantID)
	if err != nil {
		h.log.Error("list lots", "tenant_id", tenantID, "error", err.Error())
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	resp := make([]lotResponse, 0, len(lots))
	for _, l := range lots {
		resp = append(resp, toLotResponse(l))
	}
	writeJSON(w, http.StatusOK, map[string]any{"lots": resp})
}

// createLotRequest is the body of POST /api/v1/lots.
type createLotRequest struct {
	Name   string  `json:"name"`
	AreaHA float64 `json:"area_ha"`
	Crop   string  `json:"crop"`
}

// Create registers a new lot owned by the authenticated tenant.
func (h *LotsHandler) Create(w http.ResponseWriter, r *http.Request) {
	tenantID := claims.TenantIDFrom(r.Context())
	if tenantID == "" {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req createLotRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	lot, err := h.lots.Create(r.Context(), tenantID, req.Name, req.AreaHA, req.Crop)
	if errors.Is(err, domain.ErrInvalidInput) {
		writeError(w, http.StatusBadRequest, "invalid input")
		return
	}
	if err != nil {
		h.log.Error("create lot", "tenant_id", tenantID, "error", err.Error())
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	writeJSON(w, http.StatusCreated, toLotResponse(lot))
}
