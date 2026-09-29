package main

import (
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/RuleEngineLabs/engine/internal/ratelimit"
	"github.com/RuleEngineLabs/engine/internal/sandbox"
	"github.com/RuleEngineLabs/engine/internal/store"
)

func newAdminMux(ps *store.PolicyStore, rl *ratelimit.Limiter) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /policies", handleList(ps))
	mux.HandleFunc("POST /policies", handleCreate(ps))
	mux.HandleFunc("GET /policies/{id}", handleGet(ps))
	mux.HandleFunc("PUT /policies/{id}", handleUpdate(ps))
	mux.HandleFunc("DELETE /policies/{id}", handleDelete(ps))
	mux.HandleFunc("POST /execute/{id}", handleExecute(ps, rl))
	mux.HandleFunc("POST /policies/{name}/versions", handlePromote(ps))
	mux.HandleFunc("POST /policies/{name}/versions/{version}/approve", handleApproveDraft(ps))
	mux.HandleFunc("DELETE /policies/{name}/versions/{version}", handleDeleteVersion(ps))
	mux.HandleFunc("GET /policies/{name}/versions", handleListVersions(ps))
	mux.HandleFunc("GET /policies/{name}/versions/{version}", handleGetVersion(ps))
	mux.HandleFunc("PATCH /policies/{name}/meta", handleSetMeta(ps))
	mux.HandleFunc("POST /preview", handlePreview())
	mux.HandleFunc("POST /policies/{name}/canary", handleStartCanary(ps))
	mux.HandleFunc("PATCH /policies/{name}/canary", handleExtendCanary(ps))
	mux.HandleFunc("DELETE /policies/{name}/canary", handleCancelCanary(ps))
	mux.HandleFunc("POST /policies/{name}/shadow", handleStartShadow(ps))
	mux.HandleFunc("DELETE /policies/{name}/shadow", handleStopShadow(ps))
	mux.HandleFunc("GET /policies/{name}/shadow/divergences", handleGetDivergences(ps))
	mux.HandleFunc("POST /policies/{name}/shadow/promote", handlePromoteShadow(ps))
	mux.HandleFunc("GET /health", handleHealth())
	return mux
}

// registerSandboxRoutes adds the sandbox mapping publish endpoint to an existing mux.
// Call after newAdminMux when a sandbox.S3Publisher is available.
func registerSandboxRoutes(mux *http.ServeMux, ps *store.PolicyStore, pub *sandbox.S3Publisher) {
	mux.HandleFunc("PUT /policies/{name}/sandbox/connections/{service}", handlePublishSandboxMapping(ps, pub))
}

func buildServer(addr string) (string, http.Handler) {
	if addr == "" {
		addr = ":8081"
	}
	ps := store.New()
	rl := ratelimit.New(time.Second, 10)
	mux := newAdminMux(ps, rl)
	return addr, logRequests(mux)
}

func main() {
	addr, handler := buildServer(os.Getenv("ADDR"))
	slog.Info("admin starting", "addr", addr)
	if err := http.ListenAndServe(addr, handler); err != nil {
		slog.Error("admin stopped", "err", err)
		os.Exit(1)
	}
}
