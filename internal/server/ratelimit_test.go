package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

// limitedServer returns a middleware-wrapped handler whose downstream answers
// with the status chosen by path, so tests can drive the limiter end to end.
func limitedServer(t *testing.T, maxFailures string) http.Handler {
	t.Helper()
	var buf bytes.Buffer
	s := serverWithEnv(t, map[string]string{"ARTIFACTSD_AUTH_FAIL_MAX": maxFailures}, &buf)
	return s.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/c/demo/protected.txt" && r.Header.Get("Authorization") == "":
			w.Header().Set("WWW-Authenticate", `Basic realm="artifactsd"`)
			w.WriteHeader(http.StatusUnauthorized)
		case r.Header.Get("Authorization") == "Bearer bad", r.Header.Get("Authorization") == "Basic d3Jvbmc6d3Jvbmc=":
			w.WriteHeader(http.StatusUnauthorized)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
}

func hitFrom(h http.Handler, remote, path, authz string) int {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, http.NoBody)
	req.RemoteAddr = remote
	if authz != "" {
		req.Header.Set("Authorization", authz)
	}
	h.ServeHTTP(rec, req)
	return rec.Code
}

func TestRateLimit_BlocksOnlyCredentialedRequests(t *testing.T) {
	h := limitedServer(t, "3")
	const ip = "203.0.113.5:4444"
	for range 4 {
		hitFrom(h, ip, "/api/v1/collections", "Bearer bad")
	}
	cases := []struct {
		name, path, authz string
		want              int
	}{
		{"healthz", "/healthz", "", http.StatusOK},
		{"readyz", "/readyz", "", http.StatusOK},
		{"public file", "/c/demo/index.html", "", http.StatusOK},
		{"anonymous share", "/s/shr_x/", "", http.StatusOK},
		{"upload link", "/r/urq_x", "", http.StatusOK},
		{"credentialed request", "/api/v1/collections", "Bearer good", http.StatusTooManyRequests},
		{"credentialed public read", "/c/demo/index.html", "Bearer good", http.StatusTooManyRequests},
		{"admin UI", "/admin/", "", http.StatusTooManyRequests},
		{"oidc login", "/auth/oidc/login", "", http.StatusTooManyRequests},
	}
	for _, c := range cases {
		if got := hitFrom(h, ip, c.path, c.authz); got != c.want {
			t.Errorf("%s: %s = %d, want %d", c.name, c.path, got, c.want)
		}
	}
}

func TestRateLimit_AnonymousChallengeIsNotAFailure(t *testing.T) {
	h := limitedServer(t, "3")
	const ip = "203.0.113.6:4444"
	for i := range 8 {
		if got := hitFrom(h, ip, "/c/demo/protected.txt", ""); got != http.StatusUnauthorized {
			t.Fatalf("anonymous request %d = %d, want 401 (never 429)", i, got)
		}
	}
	if got := hitFrom(h, ip, "/api/v1/collections", "Bearer good"); got != http.StatusOK {
		t.Errorf("credentialed request after anonymous challenges = %d, want 200", got)
	}
}

func TestRateLimit_WrongBasicPasswordBlocks(t *testing.T) {
	h := limitedServer(t, "3")
	const ip = "203.0.113.7:4444"
	for range 3 {
		if got := hitFrom(h, ip, "/c/demo/protected.txt", "Basic d3Jvbmc6d3Jvbmc="); got != http.StatusUnauthorized {
			t.Fatalf("wrong password = %d, want 401", got)
		}
	}
	if got := hitFrom(h, ip, "/c/demo/protected.txt", "Basic d3Jvbmc6d3Jvbmc="); got != http.StatusTooManyRequests {
		t.Errorf("after 3 wrong passwords = %d, want 429", got)
	}
	if got := hitFrom(h, ip, "/c/demo/index.html", ""); got != http.StatusOK {
		t.Errorf("anonymous public read from blocked IP = %d, want 200", got)
	}
}

func TestRateLimit_IPv6SharesSlash64(t *testing.T) {
	h := limitedServer(t, "2")
	for range 2 {
		hitFrom(h, "[2001:db8:1:2::1]:5000", "/api/v1/collections", "Bearer bad")
	}
	if got := hitFrom(h, "[2001:db8:1:2:ffff::9]:5000", "/api/v1/collections", "Bearer good"); got != http.StatusTooManyRequests {
		t.Errorf("same /64 = %d, want 429", got)
	}
	if got := hitFrom(h, "[2001:db8:1:3::1]:5000", "/api/v1/collections", "Bearer good"); got != http.StatusOK {
		t.Errorf("different /64 = %d, want 200", got)
	}
}

func TestRateLimitKey(t *testing.T) {
	cases := map[string]string{
		"203.0.113.5":       "203.0.113.5",
		"2001:db8:1:2::1":   "2001:db8:1:2::/64",
		"2001:db8:1:2:a::b": "2001:db8:1:2::/64",
		"not-an-ip":         "not-an-ip",
	}
	for in, want := range cases {
		if got := rateLimitKey(in); got != want {
			t.Errorf("rateLimitKey(%q) = %q, want %q", in, got, want)
		}
	}
}
