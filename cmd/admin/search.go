package main

import (
	"encoding/json"
	"net/http"

	"github.com/RuleEngineLabs/engine/internal/store"
)

type versionMetaResponse struct {
	PolicyName  string `json:"policyName"`
	Version     int    `json:"version"`
	SemVersion  string `json:"semVersion,omitempty"`
	Status      string `json:"status"`
	ContentHash string `json:"contentHash"`
	Author      string `json:"author,omitempty"`
	CreatedAt   string `json:"createdAt"`
	RemovalNote string `json:"removalNote,omitempty"`
	Content     any    `json:"content"`
}

func toMetaResp(r *store.VersionRecord) versionMetaResponse {
	return versionMetaResponse{
		PolicyName:  r.PolicyName,
		Version:     r.Version,
		SemVersion:  r.SemVersion,
		Status:      string(r.Status),
		ContentHash: r.ContentHash,
		Author:      r.Author,
		CreatedAt:   r.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		RemovalNote: r.RemovalNote,
		Content:     r.Policy,
	}
}

func handleListVersions(ps *store.PolicyStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		recs, ok := ps.GetVersions(name)
		if !ok || len(recs) == 0 {
			writeError(w, http.StatusNotFound, "no versions found for policy")
			return
		}
		out := make([]versionMetaResponse, len(recs))
		for i, rec := range recs {
			out[i] = toMetaResp(rec)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(out)
	}
}

func handleGetVersion(ps *store.PolicyStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		version := r.PathValue("version")
		rec, ok := ps.GetVersion(name, version)
		if !ok {
			writeError(w, http.StatusNotFound, "version not found")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(toMetaResp(rec))
	}
}
