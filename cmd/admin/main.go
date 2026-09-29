package main

import (
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/RuleEngineLabs/engine/internal/ratelimit"
	"github.com/RuleEngineLabs/engine/internal/store"
)

func main() {
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8081"
	}

	ps := store.New()
	rl := ratelimit.New(time.Second, 10) // 10 noCache calls/s per caller

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

	slog.Info("admin starting", "addr", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		slog.Error("admin stopped", "err", err)
		os.Exit(1)
	}
}
