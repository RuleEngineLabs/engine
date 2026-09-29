package main

import (
	"encoding/json"
	"net/http"

	"github.com/RuleEngineLabs/engine/internal/auth"
	"github.com/RuleEngineLabs/engine/internal/sandbox"
	"github.com/RuleEngineLabs/engine/internal/store"
)

// handlePublishSandboxMapping serves PUT /policies/{name}/sandbox/connections/{service}.
// Writes a WireMock mapping directly to S3 for ad-hoc test sessions.
// Only the policy owner or policy-operators may publish overrides.
// S3 lifecycle (24h on sandbox/overrides/) handles expiry — no engine code needed.
func handlePublishSandboxMapping(ps *store.PolicyStore, pub *sandbox.S3Publisher) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		service := r.PathValue("service")

		rec, ok := ps.GetByName(name)
		if !ok {
			writeError(w, http.StatusNotFound, "policy not found")
			return
		}

		if !auth.IsOwner(r.Context(), rec.Owner) {
			writeError(w, http.StatusForbidden, "forbidden: requires owner or policy-operators group")
			return
		}

		var mapping sandbox.Mapping
		if err := json.NewDecoder(r.Body).Decode(&mapping); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}

		if err := pub.Publish(r.Context(), name, service, &mapping); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{
			"status":  "published",
			"policy":  name,
			"service": service,
		})
	}
}
