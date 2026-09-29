package main

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/RuleEngineLabs/engine/internal/store"
)

type promoteRequest struct {
	Bump string `json:"bump"`
}

type promoteResponse struct {
	Version string `json:"version"`
	Status  string `json:"status"`
}

func handlePromote(ps *store.PolicyStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")

		var req promoteRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if req.Bump == "" {
			writeError(w, http.StatusBadRequest, "bump is required")
			return
		}

		newVer, err := ps.Promote(name, req.Bump)
		if err != nil {
			var errNotApproved store.ErrDraftNotApproved
			if errors.As(err, &errNotApproved) {
				writeError(w, http.StatusConflict, errNotApproved.Error())
				return
			}
			writeError(w, http.StatusNotFound, err.Error())
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(promoteResponse{Version: newVer, Status: "STABLE"})
	}
}
