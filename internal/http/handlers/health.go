package handlers

import "net/http"

// Health answers the liveness probe. Kept dependency-free so load balancers and
// orchestrators can ping it without any DB or auth machinery.
func Health(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}
