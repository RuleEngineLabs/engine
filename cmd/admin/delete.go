package main

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/RuleEngineLabs/engine/internal/auth"
	"github.com/RuleEngineLabs/engine/internal/store"
)

type deleteVersionRequest struct {
	Reason string `json:"reason"`
}

func handleDeleteVersion(ps *store.PolicyStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		version := r.PathValue("version")

		claims := auth.FromContext(r.Context())
		var groups []string
		if claims != nil {
			groups = claims.Groups
		}

		var req deleteVersionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
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

		requestedBy := "unknown"
		if len(groups) > 0 {
			requestedBy = groups[0]
		}

		err := ps.MarkVersionRemoved(name, version, req.Reason, requestedBy)
		if err != nil {
			var errStable store.ErrCannotDeleteStable
			if errors.As(err, &errStable) {
				writeError(w, http.StatusConflict, errStable.Error())
				return
			}
			writeError(w, http.StatusNotFound, err.Error())
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"REMOVED"}`)) //nolint:errcheck
	}
}

func versionDeleteAuthorized(groups []string, owner string) bool {
	for _, g := range groups {
		if g == auth.GlobalOperatorGroup || g == owner {
			return true
		}
	}
	return false
}
