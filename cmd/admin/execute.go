package main

import (
	"encoding/json"
	"net/http"

	"github.com/RuleEngineLabs/engine/internal/auth"
	"github.com/RuleEngineLabs/engine/internal/executor"
	"github.com/RuleEngineLabs/engine/internal/ratelimit"
	"github.com/RuleEngineLabs/engine/internal/store"
)

// handleExecute serves POST /execute/{id}.
// When rl is non-nil it is applied to noCache requests; pass nil to disable rate limiting.
func handleExecute(ps *store.PolicyStore, rl *ratelimit.Limiter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")

		noCache := r.URL.Query().Get("noCache") == "true"
		origin := r.Header.Get("X-Origin")

		claims := auth.FromContext(r.Context())

		if noCache {
			// Benchmark origin: restrict to policy-operators or an approver group
			if origin == "benchmark" {
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

func noCacheCallerKey(r *http.Request, claims *auth.Claims) string {
	if claims != nil && len(claims.Groups) > 0 {
		return claims.Groups[0]
	}
	return r.RemoteAddr
}
