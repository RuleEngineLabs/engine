package main

import (
	"log/slog"
	"net/http"
	"os"
)

func main() {
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /execute/{id}", handleExecute)
	mux.HandleFunc("POST /preview", handlePreview)

	slog.Info("engine starting", "addr", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		slog.Error("engine stopped", "err", err)
		os.Exit(1)
	}
}

func handleExecute(w http.ResponseWriter, r *http.Request) {
	http.Error(w, `{"error":"not implemented"}`, http.StatusNotImplemented)
}

func handlePreview(w http.ResponseWriter, r *http.Request) {
	http.Error(w, `{"error":"not implemented"}`, http.StatusNotImplemented)
}
