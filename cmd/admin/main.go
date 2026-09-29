package main

import (
	"log/slog"
	"net/http"
	"os"

	"github.com/RuleEngineLabs/engine/internal/store"
)

func main() {
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8081"
	}

	ps := store.New()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /policies", handleList(ps))
	mux.HandleFunc("POST /policies", handleCreate(ps))
	mux.HandleFunc("GET /policies/{id}", handleGet(ps))
	mux.HandleFunc("PUT /policies/{id}", handleUpdate(ps))
	mux.HandleFunc("DELETE /policies/{id}", handleDelete(ps))
	mux.HandleFunc("POST /execute/{id}", handleExecute(ps))
	mux.HandleFunc("POST /policies/{name}/versions", handlePromote(ps))

	slog.Info("admin starting", "addr", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		slog.Error("admin stopped", "err", err)
		os.Exit(1)
	}
}
