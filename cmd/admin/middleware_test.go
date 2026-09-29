package main

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// logBuffer captures slog output for assertions.
func newLogBuffer() (*bytes.Buffer, func()) {
	buf := &bytes.Buffer{}
	h := slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	old := slog.Default()
	slog.SetDefault(slog.New(h))
	return buf, func() { slog.SetDefault(old) }
}

func TestLogRequests_200_LogsInfo(t *testing.T) {
	buf, restore := newLogBuffer()
	defer restore()

	handler := logRequests(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	log := buf.String()
	if !strings.Contains(log, "INFO") {
		t.Fatalf("expected INFO level for 200, got: %s", log)
	}
	if !strings.Contains(log, "GET") || !strings.Contains(log, "/health") {
		t.Fatalf("expected method+path in log, got: %s", log)
	}
	if !strings.Contains(log, "status=200") {
		t.Fatalf("expected status=200 in log, got: %s", log)
	}
}

func TestLogRequests_404_LogsWarn(t *testing.T) {
	buf, restore := newLogBuffer()
	defer restore()

	handler := logRequests(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))

	req := httptest.NewRequest(http.MethodGet, "/unknown", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	log := buf.String()
	if !strings.Contains(log, "WARN") {
		t.Fatalf("expected WARN level for 404, got: %s", log)
	}
	if !strings.Contains(log, "status=404") {
		t.Fatalf("expected status=404 in log, got: %s", log)
	}
}

func TestLogRequests_500_LogsError(t *testing.T) {
	buf, restore := newLogBuffer()
	defer restore()

	handler := logRequests(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))

	req := httptest.NewRequest(http.MethodPost, "/execute/x", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	log := buf.String()
	if !strings.Contains(log, "ERROR") {
		t.Fatalf("expected ERROR level for 500, got: %s", log)
	}
	if !strings.Contains(log, "status=500") {
		t.Fatalf("expected status=500 in log, got: %s", log)
	}
}

func TestLogRequests_ImplicitWrite_CapturesStatus(t *testing.T) {
	buf, restore := newLogBuffer()
	defer restore()

	// Handler writes body without calling WriteHeader — status defaults to 200.
	handler := logRequests(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok")) //nolint:errcheck
	}))

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	log := buf.String()
	if !strings.Contains(log, "status=200") {
		t.Fatalf("expected status=200 for implicit write, got: %s", log)
	}
}

func TestLogRequests_DurationPresent(t *testing.T) {
	buf, restore := newLogBuffer()
	defer restore()

	handler := logRequests(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/policies", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if !strings.Contains(buf.String(), "duration_ms=") {
		t.Fatalf("expected duration_ms in log, got: %s", buf.String())
	}
}

func TestLogRequests_IntegrationWithMux(t *testing.T) {
	buf, restore := newLogBuffer()
	defer restore()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`)) //nolint:errcheck
	})

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()
	logRequests(mux).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rr.Code)
	}
	if !strings.Contains(buf.String(), "/health") {
		t.Fatalf("expected /health in log")
	}
}
