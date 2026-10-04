package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/RuleEngineLabs/engine/internal/auth"
	"github.com/RuleEngineLabs/engine/internal/executor"
	"github.com/RuleEngineLabs/engine/internal/ratelimit"
	"github.com/RuleEngineLabs/engine/internal/store"
)


// executeResponse is the envelope returned on successful policy execution.
// It mirrors executor.Result fields and adds duration_ms without modifying the executor type.
type executeResponse struct {
	State      string               `json:"state"`
	Data       any                  `json:"data"`
	Trace      []executor.TraceEntry `json:"trace,omitempty"`
	DurationMs int64                `json:"duration_ms"`
}

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
					w.Header().Set("Retry-After", "1")
					writeError(w, http.StatusTooManyRequests, "rate limit exceeded for noCache")
					return
				}
			}
		}

		rec, err := ps.Current(id)
		if err != nil {
			writeError(w, http.StatusNotFound, "policy_not_found")
			return
		}

		meta := ps.GetMeta(rec.Name)

		var input any
		if r.ContentLength > 0 {
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				writeError(w, http.StatusBadRequest, "invalid JSON body")
				return
			}
		}

		// Apply per-policy execution timeout.
		execCtx := r.Context()
		if meta != nil && meta.HTTP.TimeoutMs > 0 {
			var cancel context.CancelFunc
			execCtx, cancel = context.WithTimeout(execCtx, time.Duration(meta.HTTP.TimeoutMs)*time.Millisecond)
			defer cancel()
		}

		log := loggerFromContext(r.Context())
		start := time.Now()
		result, err := executor.Execute(execCtx, rec.Artifact, input)
		durationMs := time.Since(start).Milliseconds()
		if err != nil {
			log.Error("execute failed", "policy", rec.Name, "err", err)
			writeError(w, http.StatusInternalServerError, "internal execution error")
			return
		}
		log.Info("execute ok", "policy", rec.Name, "state", result.State, "duration_ms", durationMs)

		// Fire shadow execution asynchronously — consumer always gets STABLE response.
		if shadow, ok := ps.GetActiveShadow(rec.Name); ok {
			go runShadow(ps, rec.Name, rec.StableVersion, shadow, input, result, log)
		}

		// Resolve HTTP status: policy state wins; fall back to 200.
		httpStatus := http.StatusOK
		if result.Status != 0 {
			httpStatus = result.Status
		}

		w.Header().Set("Content-Type", "application/json")

		// Cache-Control: only on GET requests (semantically cacheable).
		if meta != nil && meta.HTTP.CacheMaxAgeSeconds > 0 && r.Method == http.MethodGet {
			w.Header().Set("Cache-Control", fmt.Sprintf("max-age=%d", meta.HTTP.CacheMaxAgeSeconds))
		}

		// Retry-After: when the resolved status is in the configured retry list.
		if meta != nil && meta.HTTP.RetryAfterSeconds > 0 {
			for _, code := range meta.HTTP.RetryOn {
				if code == httpStatus {
					w.Header().Set("Retry-After", strconv.Itoa(meta.HTTP.RetryAfterSeconds))
					break
				}
			}
		}

		w.WriteHeader(httpStatus)
		json.NewEncoder(w).Encode(executeResponse{
			State:      result.State,
			Data:       result.Data,
			Trace:      result.Trace,
			DurationMs: durationMs,
		})
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
func runShadow(ps *store.PolicyStore, policyName, stableVersion string, shadow *store.ShadowRecord, input any, stableResult executor.Result, log *slog.Logger) {
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
		log.Warn("shadow divergence (candidate error)",
			"policy", policyName,
			"stable_version", stableVersion,
			"candidate_version", shadow.CandidateVersion,
			"err", err,
		)
		return
	}

	stableJSON, _ := json.Marshal(stableResult.Data)
	candJSON, _ := json.Marshal(candResult.Data)
	if !bytes.Equal(stableJSON, candJSON) {
		d.CandidateOutput = candResult.Data
		ps.LogDivergence(d)
		log.Warn("shadow divergence (output mismatch)",
			"policy", policyName,
			"stable_version", stableVersion,
			"candidate_version", shadow.CandidateVersion,
		)
	}
	// Identical outputs: no divergence record.
}

func noCacheCallerKey(r *http.Request, claims *auth.Claims) string {
	if claims != nil && len(claims.Groups) > 0 {
		return claims.Groups[0]
	}
	return r.RemoteAddr
}
