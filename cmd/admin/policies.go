package main

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/RuleEngineLabs/engine/internal/compiler"
	"github.com/RuleEngineLabs/engine/internal/policy"
	"github.com/RuleEngineLabs/engine/internal/store"
)

type createPolicyRequest struct {
	Name   string         `json:"name"`
	Entry  string         `json:"entry"`
	States []policy.State `json:"states"`
}

type createPolicyResponse struct {
	PolicyID string `json:"policyId"`
	Version  int    `json:"version"`
}

type errorResponse struct {
	Error string `json:"error"`
}

func handleCreate(ps *store.PolicyStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req createPolicyRequest
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

		owner := r.Header.Get("X-Owner-Group")
		policyID, version, err := ps.Create(req.Name, owner, art)
		if err != nil {
			var errReserved store.ErrNameReserved
			if errors.As(err, &errReserved) {
				writeError(w, http.StatusUnprocessableEntity, errReserved.Error())
			} else {
				writeError(w, http.StatusConflict, err.Error())
			}
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(createPolicyResponse{PolicyID: policyID, Version: version})
	}
}

func handleList(ps *store.PolicyStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		records := ps.List()
		out := make([]map[string]any, 0, len(records))
		for _, rec := range records {
			out = append(out, map[string]any{
				"policyId": rec.PolicyID,
				"name":     rec.Name,
				"version":  rec.Version,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(out)
	}
}

func handleGet(ps *store.PolicyStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		rec, err := ps.Get(id)
		if err != nil {
			writeError(w, http.StatusNotFound, "policy not found")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"policyId": rec.PolicyID,
			"name":     rec.Name,
			"version":  rec.Version,
			"entry":    rec.Policy.Entry,
			"states":   rec.Policy.States,
		})
	}
}

func handleUpdate(ps *store.PolicyStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotImplemented, "not implemented")
	}
}

func handleDelete(ps *store.PolicyStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotImplemented, "not implemented")
	}
}

func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(errorResponse{Error: msg})
}
