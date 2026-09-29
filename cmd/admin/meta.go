package main

import (
	"encoding/json"
	"net/http"

	"github.com/RuleEngineLabs/engine/internal/store"
)

type patchMetaRequest struct {
	CoexistenceWindowSeconds int `json:"coexistence_window_seconds"`
}

func handleSetMeta(ps *store.PolicyStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")

		var req patchMetaRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}

		err := ps.SetMeta(name, &store.PolicyMeta{
			CoexistenceWindowSeconds: req.CoexistenceWindowSeconds,
		})
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(req)
	}
}
