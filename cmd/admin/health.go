package main

import (
	"encoding/json"
	"net/http"
)

// handleHealth serves GET /health.
// Returns 200 {"status":"ok"} — used by the ALB health check in production.
func handleHealth() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}
}
