package handlers

import (
	"log/slog"
	"net/http"

	"github.com/ezequielranieri/agro-iam/internal/application/ports"
)

// TenantsHandler exposes the public realm list (AP2). The route carries no
// auth or role guard: the login screen must be able to render the tenant
// selector before any credentials exist.
type TenantsHandler struct {
	tenants ports.TenantRepository
	log     *slog.Logger
}

// NewTenantsHandler wires the handler.
func NewTenantsHandler(tenants ports.TenantRepository, log *slog.Logger) *TenantsHandler {
	return &TenantsHandler{tenants: tenants, log: log}
}

// tenantResponse is the JSON shape of one realm entry. AP2 is explicit: id+name
// only — never credentials, never internal columns. Any future column must
// opt-in here, not leak through.
type tenantResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// List returns the realm catalog, read at request time so the ids survive
// reseeds (no hardcoded uuids in the login screen).
func (h *TenantsHandler) List(w http.ResponseWriter, r *http.Request) {
	tenants, err := h.tenants.List(r.Context())
	if err != nil {
		h.log.Error("list tenants", "error", err.Error())
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	resp := make([]tenantResponse, 0, len(tenants))
	for _, t := range tenants {
		resp = append(resp, tenantResponse{ID: t.ID, Name: t.Name})
	}
	writeJSON(w, http.StatusOK, map[string]any{"tenants": resp})
}
