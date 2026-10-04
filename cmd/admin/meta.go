package main

import (
	"encoding/json"
	"net/http"

	"github.com/RuleEngineLabs/engine/internal/store"
)

type patchMetaRequest struct {
	CoexistenceWindowSeconds int      `json:"coexistence_window_seconds"`
	Approvers                []string `json:"approvers"`
	HTTP                     struct {
		RetryOn            []int `json:"retry_on,omitempty"`
		RetryAfterSeconds  int   `json:"retry_after_seconds,omitempty"`
		TimeoutMs          int   `json:"timeout_ms,omitempty"`
		CacheMaxAgeSeconds int   `json:"cache_max_age_seconds,omitempty"`
	} `json:"http,omitempty"`
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
			Approvers:                req.Approvers,
			HTTP: store.HTTPConfig{
				RetryOn:            req.HTTP.RetryOn,
				RetryAfterSeconds:  req.HTTP.RetryAfterSeconds,
				TimeoutMs:          req.HTTP.TimeoutMs,
				CacheMaxAgeSeconds: req.HTTP.CacheMaxAgeSeconds,
			},
		})
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(req)
	}
}
