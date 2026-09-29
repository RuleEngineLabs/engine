package main

import (
	"errors"
	"net/http"

	"github.com/RuleEngineLabs/engine/internal/auth"
	"github.com/RuleEngineLabs/engine/internal/store"
)

func handleApproveDraft(ps *store.PolicyStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		claims := auth.FromContext(r.Context())
		var groups []string
		if claims != nil {
			groups = claims.Groups
		}

		err := ps.ApproveDraftByVersion(name, groups)
		if err != nil {
			var errForbidden store.ErrApprovalForbidden
			if errors.As(err, &errForbidden) {
				writeError(w, http.StatusForbidden, errForbidden.Error())
				return
			}
			writeError(w, http.StatusNotFound, err.Error())
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"APPROVED"}`)) //nolint:errcheck
	}
}
