package main

import (
	"encoding/json"
	"net/http"

	"github.com/RuleEngineLabs/engine/internal/compiler"
	"github.com/RuleEngineLabs/engine/internal/executor"
	"github.com/RuleEngineLabs/engine/internal/policy"
)

type previewRequest struct {
	Name   string         `json:"name"`
	Entry  string         `json:"entry"`
	States []policy.State `json:"states"`
	Input  any            `json:"input"`
}

func handlePreview() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req previewRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}

		p := &policy.Policy{
			Name:   req.Name,
			Entry:  req.Entry,
			States: req.States,
		}

		art, err := compiler.Compile(p)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}

		result, err := executor.Preview(r.Context(), art, req.Input)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	}
}
