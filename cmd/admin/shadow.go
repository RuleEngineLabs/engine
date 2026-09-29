package main

import (
	"encoding/json"
	"errors"
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
			var alreadyActive store.ErrShadowAlreadyActive
			if errors.As(err, &alreadyActive) {
				writeError(w, http.StatusConflict, err.Error())
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
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
			var notFound store.ErrShadowNotFound
			if errors.As(err, &notFound) {
				writeError(w, http.StatusNotFound, err.Error())
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(rec)
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
