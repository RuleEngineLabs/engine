package main

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/RuleEngineLabs/engine/internal/auth"
	"github.com/RuleEngineLabs/engine/internal/store"
)

type startCanaryRequest struct {
	CandidateVersion string `json:"candidateVersion"`
	Percent          int    `json:"percent"`
	TTLSeconds       int    `json:"ttl"`
}

type cancelCanaryRequest struct {
	Reason string `json:"reason"`
}

func handleStartCanary(ps *store.PolicyStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")

		claims := auth.FromContext(r.Context())
		var groups []string
		if claims != nil {
			groups = claims.Groups
		}

		rec, ok := ps.GetByName(name)
		if !ok {
			writeError(w, http.StatusNotFound, "policy not found")
			return
		}

		if !versionDeleteAuthorized(groups, rec.Owner) {
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}

		var req startCanaryRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}

		canary, err := ps.StartCanary(name, req.CandidateVersion, req.Percent, req.TTLSeconds)
		if err != nil {
			var errPercent store.ErrCanaryInvalidPercent
			if errors.As(err, &errPercent) {
				writeError(w, http.StatusUnprocessableEntity, errPercent.Error())
				return
			}
			var errActive store.ErrCanaryAlreadyActive
			if errors.As(err, &errActive) {
				writeError(w, http.StatusConflict, errActive.Error())
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{
			"policyName":       canary.PolicyName,
			"candidateVersion": canary.CandidateVersion,
			"percent":          canary.Percent,
			"ttl":              canary.TTLSeconds,
			"status":           canary.Status,
			"gsiActiveStatus":  canary.GsiActiveStatus,
			"expiresAt":        canary.ExpiresAt,
		})
	}
}

func handleCancelCanary(ps *store.PolicyStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")

		claims := auth.FromContext(r.Context())
		var groups []string
		if claims != nil {
			groups = claims.Groups
		}

		rec, ok := ps.GetByName(name)
		if !ok {
			writeError(w, http.StatusNotFound, "policy not found")
			return
		}

		if !versionDeleteAuthorized(groups, rec.Owner) {
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}

		var req cancelCanaryRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}

		canary, err := ps.CancelCanary(name, req.Reason)
		if err != nil {
			var errNotFound store.ErrCanaryNotFound
			if errors.As(err, &errNotFound) {
				writeError(w, http.StatusNotFound, errNotFound.Error())
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"policyName":      canary.PolicyName,
			"status":          canary.Status,
			"gsiActiveStatus": canary.GsiActiveStatus,
			"cancelReason":    canary.CancelReason,
		})
	}
}
