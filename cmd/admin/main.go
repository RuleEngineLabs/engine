package main

import (
	"log/slog"
	"net/http"
	"os"
)

func main() {
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8081"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /policies", handleList)
	mux.HandleFunc("POST /policies", handleCreate)
	mux.HandleFunc("GET /policies/{id}", handleGet)
	mux.HandleFunc("PUT /policies/{id}", handleUpdate)
	mux.HandleFunc("DELETE /policies/{id}", handleDelete)

	slog.Info("admin starting", "addr", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		slog.Error("admin stopped", "err", err)
		os.Exit(1)
	}
}

func handleList(w http.ResponseWriter, r *http.Request)   { stub(w) }
func handleCreate(w http.ResponseWriter, r *http.Request) { stub(w) }
func handleGet(w http.ResponseWriter, r *http.Request)    { stub(w) }
func handleUpdate(w http.ResponseWriter, r *http.Request) { stub(w) }
func handleDelete(w http.ResponseWriter, r *http.Request) { stub(w) }

func stub(w http.ResponseWriter) {
	http.Error(w, `{"error":"not implemented"}`, http.StatusNotImplemented)
}
