package main

import (
	"encoding/json"
	"net/http"

	"github.com/RuleEngineLabs/engine/internal/store"
)

// handleStartShadow serves POST /policies/{name}/shadow.
// Uses the policy's current draft as the candidate artifact.
func handleStartShadow(ps *store.PolicyStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")

		var req struct {
			CandidateVersion string `json:"candidateVersion"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}

		draft, ok := ps.GetDraft(name)
		if !ok {
			writeError(w, http.StatusNotFound, "no draft found for policy")
			return
		}

		candidateVersion := req.CandidateVersion
		if candidateVersion == "" {
			candidateVersion = "draft"
		}

		rec, err := ps.StartShadow(name, candidateVersion, draft.Artifact)
		if err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(rec)
	}
}

// handleStopShadow serves DELETE /policies/{name}/shadow.
func handleStopShadow(ps *store.PolicyStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")

		rec, err := ps.StopShadow(name)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(rec)
	}
}

// handlePromoteShadow serves POST /policies/{name}/shadow/promote.
// Promotes the shadow candidate to STABLE.
// If divergences exist without force=true, returns 409 with the divergence list.
func handlePromoteShadow(ps *store.PolicyStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")

		var req struct {
			Force bool   `json:"force"`
			Bump  string `json:"bump"` // patch | minor | major; defaults to minor
		}
		if r.ContentLength > 0 {
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeError(w, http.StatusBadRequest, "invalid JSON body")
				return
			}
		}

		_, ok := ps.GetActiveShadow(name)
		if !ok {
			writeError(w, http.StatusNotFound, "no active shadow for policy")
			return
		}

		divs := ps.GetDivergences(name)
		if len(divs) > 0 && !req.Force {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]any{
				"error":       "divergences exist; set force=true to promote anyway",
				"divergences": divs,
			})
			return
		}

		bump := req.Bump
		if bump == "" {
			bump = "minor"
		}

		if err := ps.ApproveDraft(name); err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}

		newVersion, err := ps.Promote(name, bump)
		if err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}

		// Non-fatal if shadow was already stopped externally.
		ps.StopShadow(name) //nolint:errcheck

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"promoted": true,
			"version":  newVersion,
		})
	}
}

// handleGetDivergences serves GET /policies/{name}/shadow/divergences.
func handleGetDivergences(ps *store.PolicyStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		divs := ps.GetDivergences(name)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(divs)
	}
}
