package handlers

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/ezequielranieri/agro-iam/internal/application/ports"
	"github.com/ezequielranieri/agro-iam/internal/domain"
	"github.com/ezequielranieri/agro-iam/internal/http/claims"
)

// auditDefaultLimit is the latest-N window of GET /api/v1/audit (AP1): the
// demo audit screen renders the most recent events, not the full trail.
const auditDefaultLimit = 100

// AuditHandler exposes the admin-only audit-trail read endpoint (AP1). The
// tenant id comes from the authenticated claims — never from the client — and
// the route is gated by requireRole(admin), so only admins reach the handler.
type AuditHandler struct {
	audit ports.AuditService
	log   *slog.Logger
}

// NewAuditHandler wires the handler.
func NewAuditHandler(audit ports.AuditService, log *slog.Logger) *AuditHandler {
	return &AuditHandler{audit: audit, log: log}
}

// auditEventResponse is the JSON shape of one audit entry for the demo table:
// the event, severity, actor and timestamp columns the screen renders. The
// payload blob is deliberately omitted — the trail's payload is not needed by
// the UI and keeping it out keeps the response lean.
type auditEventResponse struct {
	Seq         int64  `json:"seq"`
	ActorUserID string `json:"actor_user_id"`
	Action      string `json:"action"`
	EntityType  string `json:"entity_type"`
	EntityID    string `json:"entity_id"`
	Severity    string `json:"severity"`
	CreatedAt   string `json:"created_at"`
}

func toAuditEventResponse(e *domain.AuditEntry) auditEventResponse {
	return auditEventResponse{
		Seq:         e.Seq,
		ActorUserID: e.ActorUserID,
		Action:      e.Action,
		EntityType:  e.EntityType,
		EntityID:    e.EntityID,
		Severity:    e.Severity,
		CreatedAt:   e.CreatedAt.UTC().Format(time.RFC3339),
	}
}

// Latest returns the tenant's most recent audit events, newest first (AP1).
// The route is already admin-gated by the middleware; this handler only
// translates claims + service output onto the HTTP contract.
func (h *AuditHandler) Latest(w http.ResponseWriter, r *http.Request) {
	tenantID := claims.TenantIDFrom(r.Context())
	if tenantID == "" {
		// Reached without the middleware's claims: the request was never
		// authenticated, so 401 rather than 500.
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	entries, err := h.audit.Latest(r.Context(), tenantID, auditDefaultLimit)
	if err != nil {
		writeAuditError(w, h.log, "list audit", tenantID, err)
		return
	}

	resp := make([]auditEventResponse, 0, len(entries))
	for _, e := range entries {
		resp = append(resp, toAuditEventResponse(e))
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": resp})
}

// writeAuditError maps service errors onto the HTTP contract: missing rows
// 404, tenant required 401; any other error is logged and collapsed to a 500.
func writeAuditError(w http.ResponseWriter, log *slog.Logger, op, tenantID string, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, domain.ErrTenantRequired):
		writeError(w, http.StatusUnauthorized, "unauthorized")
	default:
		log.Error(op, "tenant_id", tenantID, "error", err.Error())
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}
