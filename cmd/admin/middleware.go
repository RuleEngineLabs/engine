package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

type contextKey int

const loggerKey contextKey = iota

// responseRecorder wraps http.ResponseWriter to capture the status code written.
type responseRecorder struct {
	http.ResponseWriter
	status int
}

func (r *responseRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Write captures a 200 when WriteHeader was never called explicitly.
func (r *responseRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(b)
}

func newRequestID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// withCorrelationID reads X-Request-ID from the request (or generates one),
// attaches a scoped *slog.Logger with "request_id" to the context,
// and echoes the ID in the X-Request-ID response header.
func withCorrelationID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" {
			id = newRequestID()
		}
		w.Header().Set("X-Request-ID", id)
		logger := slog.Default().With("request_id", id)
		ctx := context.WithValue(r.Context(), loggerKey, logger)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// loggerFromContext returns the scoped logger stored by withCorrelationID,
// falling back to slog.Default() when none is present.
func loggerFromContext(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(loggerKey).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}

// logRequests wraps a handler and emits a structured log line per request.
// Uses the context logger (set by withCorrelationID) so every line carries request_id.
func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &responseRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)

		status := rec.status
		if status == 0 {
			status = http.StatusOK
		}
		ms := time.Since(start).Milliseconds()

		log := loggerFromContext(r.Context())
		args := []any{
			"method", r.Method,
			"path", r.URL.Path,
			"status", status,
			"duration_ms", ms,
		}

		switch {
		case status >= 500:
			log.Error("request", args...)
		case status >= 400:
			log.Warn("request", args...)
		default:
			log.Info("request", args...)
		}
	})
}
