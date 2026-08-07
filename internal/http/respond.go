package http

import (
	"encoding/json"
	"net/http"
)

// writeJSON writes payload as JSON with the given status. The encode error is
// deliberately swallowed: every payload here is a fixed struct or map that
// cannot fail to serialize, and a half-written response cannot be un-sent.
func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// writeError writes a JSON error body matching the handlers' writeError
// convention, so the middleware's 401s are indistinguishable from handler
// errors on the wire.
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
