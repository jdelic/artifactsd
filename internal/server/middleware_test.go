package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jdelic/artifactsd/internal/audit"
	"github.com/jdelic/artifactsd/internal/config"
)

// serverWithEnv builds a Server with extra env (e.g. low rate-limit) and an
// access logger writing to buf.
func serverWithEnv(t *testing.T, env map[string]string, buf *bytes.Buffer) *Server {
	t.Helper()
	base := map[string]string{"ARTIFACTSD_BASE_URL": "https://x.example.com", "ARTIFACTSD_DATA_DIR": t.TempDir()}
	for k, v := range env {
		base[k] = v
	}
	cfg, err := config.LoadAndValidate(nil, func(k string) string { return base[k] }, nil)
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	return New(Options{Config: cfg, Logger: audit.NewAppLogger(buf, "error"), Access: audit.NewAccessLogger(buf)})
}

func TestMiddleware_Recovery(t *testing.T) {
	var buf bytes.Buffer
	s := serverWithEnv(t, nil, &buf)
	h := s.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", http.NoBody))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("panic status = %d, want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "boom") {
		t.Error("panic message leaked to client")
	}
}

func TestMiddleware_RequestID(t *testing.T) {
	var buf bytes.Buffer
	s := serverWithEnv(t, nil, &buf)
	var seen string
	h := s.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = audit.RequestIDFromContext(r.Context())
	}))

	// Generated when absent.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", http.NoBody))
	if rec.Header().Get("X-Request-Id") == "" || seen == "" {
		t.Error("request id not generated/propagated")
	}

	// Honored when provided.
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", http.NoBody)
	req.Header.Set("X-Request-Id", "abc123")
	h.ServeHTTP(rec, req)
	if seen != "abc123" || rec.Header().Get("X-Request-Id") != "abc123" {
		t.Errorf("inbound request id not honored: ctx=%q hdr=%q", seen, rec.Header().Get("X-Request-Id"))
	}
}

func TestMiddleware_AccessLog(t *testing.T) {
	var buf bytes.Buffer
	s := serverWithEnv(t, nil, &buf)
	h := s.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/c/demo/x", http.NoBody))

	out := buf.String()
	if !strings.Contains(out, `"status":418`) {
		t.Errorf("access log missing status: %s", out)
	}
	if !strings.Contains(out, `"collection":"demo"`) {
		t.Errorf("access log missing collection: %s", out)
	}
}

func TestMiddleware_RateLimit(t *testing.T) {
	var buf bytes.Buffer
	s := serverWithEnv(t, map[string]string{"ARTIFACTSD_AUTH_FAIL_MAX": "2"}, &buf)
	h := s.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))

	call := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/x", http.NoBody)
		req.RemoteAddr = "203.0.113.5:4444"
		h.ServeHTTP(rec, req)
		return rec
	}

	// Two failures are allowed (return 401), the third is blocked (429).
	if got := call().Code; got != http.StatusUnauthorized {
		t.Errorf("call 1 = %d, want 401", got)
	}
	if got := call().Code; got != http.StatusUnauthorized {
		t.Errorf("call 2 = %d, want 401", got)
	}
	blocked := call()
	if blocked.Code != http.StatusTooManyRequests {
		t.Errorf("call 3 = %d, want 429", blocked.Code)
	}
	if blocked.Header().Get("Retry-After") == "" {
		t.Error("429 missing Retry-After header")
	}
}

func TestMiddleware_RateLimit_IsolatesIPs(t *testing.T) {
	var buf bytes.Buffer
	s := serverWithEnv(t, map[string]string{"ARTIFACTSD_AUTH_FAIL_MAX": "1"}, &buf)
	h := s.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	hit := func(ip string) int {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/x", http.NoBody)
		req.RemoteAddr = ip + ":1000"
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	hit("198.51.100.1") // uses up A's single token
	if code := hit("198.51.100.1"); code != http.StatusTooManyRequests {
		t.Errorf("A second call = %d, want 429", code)
	}
	if code := hit("198.51.100.2"); code != http.StatusForbidden {
		t.Errorf("B first call = %d, want 403 (isolated)", code)
	}
}
