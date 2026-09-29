package main

import (
	"log/slog"
	"net/http"
	"time"
)

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

// logRequests wraps a handler and emits a structured slog line per request:
//
//	level=INFO  msg=request method=POST path=/execute/abc status=200 duration_ms=3
//	level=WARN  msg=request method=GET  path=/unknown    status=404 duration_ms=0
//	level=ERROR msg=request method=POST path=/preview    status=500 duration_ms=1
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

		args := []any{
			"method", r.Method,
			"path", r.URL.Path,
			"status", status,
			"duration_ms", ms,
		}

		switch {
		case status >= 500:
			slog.Error("request", args...)
		case status >= 400:
			slog.Warn("request", args...)
		default:
			slog.Info("request", args...)
		}
	})
}
