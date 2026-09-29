package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"time"

	"github.com/RuleEngineLabs/engine/internal/auth"
	"github.com/RuleEngineLabs/engine/internal/executor"
	"github.com/RuleEngineLabs/engine/internal/ratelimit"
	"github.com/RuleEngineLabs/engine/internal/store"
)

// isStaging reports whether the current environment is staging.
// Reads ENVIRONMENT env var; accepts "staging" (case-insensitive) or "homologacao".
// Exported as a variable so tests can override it.
var isStaging = func() bool {
	e := os.Getenv("ENVIRONMENT")
	return e == "staging" || e == "homologacao"
}

// handleExecute serves POST /execute/{id}.
// When rl is non-nil it is applied to noCache requests; pass nil to disable rate limiting.
func handleExecute(ps *store.PolicyStore, rl *ratelimit.Limiter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")

		noCache := r.URL.Query().Get("noCache") == "true"
		origin := r.Header.Get("X-Origin")

		claims := auth.FromContext(r.Context())

		if noCache {
			// Benchmark origin: restricted to approvers AND staging environment only.
			if origin == "benchmark" {
				if !isStaging() {
					writeError(w, http.StatusForbidden, "forbidden: benchmark origin restricted to staging")
					return
				}
				if !isOperatorOrApprover(claims) {
					writeError(w, http.StatusForbidden, "forbidden: benchmark noCache requires approver role")
					return
				}
			}

			// Rate limit all noCache calls
			if rl != nil {
				key := noCacheCallerKey(r, claims)
				if !rl.Allow(key) {
					writeError(w, http.StatusTooManyRequests, "rate limit exceeded for noCache")
					return
				}
			}
		}

		rec, err := ps.Get(id)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": "policy_not_found"})
			return
		}

		var input any
		if r.ContentLength > 0 {
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				writeError(w, http.StatusBadRequest, "invalid JSON body")
				return
			}
		}

		result, err := executor.Execute(r.Context(), rec.Artifact, input)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		// Fire shadow execution asynchronously — consumer always gets STABLE response.
		if shadow, ok := ps.GetActiveShadow(rec.Name); ok {
			go runShadow(ps, rec.Name, rec.StableVersion, shadow, input, result)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	}
}

func isOperatorOrApprover(claims *auth.Claims) bool {
	if claims == nil {
		return false
	}
	for _, g := range claims.Groups {
		if g == auth.GlobalOperatorGroup {
			return true
		}
	}
	return false
}

// runShadow executes the candidate artifact in a goroutine and logs a divergence
// if its output differs from the STABLE result (or if the candidate errors).
func runShadow(ps *store.PolicyStore, policyName, stableVersion string, shadow *store.ShadowRecord, input any, stableResult executor.Result) {
	candResult, err := executor.Execute(context.Background(), shadow.CandidateArtifact, input)

	d := &store.ShadowDivergence{
		Timestamp:        time.Now(),
		PolicyName:       policyName,
		StableVersion:    stableVersion,
		CandidateVersion: shadow.CandidateVersion,
		StableOutput:     stableResult.Data,
	}

	if err != nil {
		d.CandidateError = err.Error()
		ps.LogDivergence(d)
		return
	}

	stableJSON, _ := json.Marshal(stableResult.Data)
	candJSON, _ := json.Marshal(candResult.Data)
	if !bytes.Equal(stableJSON, candJSON) {
		d.CandidateOutput = candResult.Data
		ps.LogDivergence(d)
	}
	// Identical outputs: no divergence record.
}

func noCacheCallerKey(r *http.Request, claims *auth.Claims) string {
	if claims != nil && len(claims.Groups) > 0 {
		return claims.Groups[0]
	}
	return r.RemoteAddr
}
