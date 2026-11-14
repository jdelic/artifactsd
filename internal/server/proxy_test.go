package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jdelic/artifactsd/internal/audit"
)

func TestSecureAndScheme(t *testing.T) {
	cases := []struct {
		name       string
		env        map[string]string
		remote     string
		xfProto    string
		wantSecure bool
	}{
		{"plain http no proxy", nil, "10.0.0.1:5000", "", false},
		{"xfp https but no trusted proxies", nil, "10.0.0.1:5000", "https", false},
		{"xfp https from trusted proxy", map[string]string{"ARTIFACTSD_TRUSTED_PROXIES": "10.0.0.0/8"}, "10.0.0.1:5000", "https", true},
		{"xfp https from untrusted peer", map[string]string{"ARTIFACTSD_TRUSTED_PROXIES": "10.0.0.0/8"}, "198.51.100.1:5000", "https", false},
		{"xfp http from trusted proxy", map[string]string{"ARTIFACTSD_TRUSTED_PROXIES": "10.0.0.0/8"}, "10.0.0.1:5000", "http", false},
		{"deprecated trust-proxy, private peer", map[string]string{"ARTIFACTSD_TRUST_PROXY": "true"}, "10.0.0.1:5000", "https", true},
		{"deprecated trust-proxy, public peer", map[string]string{"ARTIFACTSD_TRUST_PROXY": "true"}, "198.51.100.1:5000", "https", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var buf bytes.Buffer
			s := serverWithEnv(t, c.env, &buf)
			r := httptest.NewRequest(http.MethodGet, "/x", http.NoBody)
			r.RemoteAddr = c.remote
			if c.xfProto != "" {
				r.Header.Set("X-Forwarded-Proto", c.xfProto)
			}
			if got := s.Secure(r); got != c.wantSecure {
				t.Errorf("Secure = %v, want %v", got, c.wantSecure)
			}
			wantScheme := "http"
			if c.wantSecure {
				wantScheme = "https"
			}
			if got := s.Scheme(r); got != wantScheme {
				t.Errorf("Scheme = %q, want %q", got, wantScheme)
			}
		})
	}
}

func TestClientIP_TrustedProxies(t *testing.T) {
	var buf bytes.Buffer
	s := serverWithEnv(t, map[string]string{"ARTIFACTSD_TRUSTED_PROXIES": "10.0.0.0/8"}, &buf)
	r := httptest.NewRequest(http.MethodGet, "/x", http.NoBody)
	r.RemoteAddr = "10.0.0.1:5000"
	// The left element is client-supplied; the proxy appended 203.0.113.9.
	r.Header.Set("X-Forwarded-For", "192.0.2.66, 203.0.113.9")
	if got := s.clientIP(r); got != "203.0.113.9" {
		t.Errorf("clientIP behind trusted proxy = %q, want 203.0.113.9", got)
	}

	// Without trusted proxies, the header is ignored.
	s2 := serverWithEnv(t, nil, &buf)
	if got := s2.clientIP(r); got != "10.0.0.1" {
		t.Errorf("clientIP without trusted proxies = %q, want 10.0.0.1", got)
	}

	// A direct client cannot spoof its address.
	r.RemoteAddr = "198.51.100.1:5000"
	if got := s.clientIP(r); got != "198.51.100.1" {
		t.Errorf("clientIP from untrusted peer = %q, want 198.51.100.1", got)
	}
}

// TestHostIsolation_ForwardedHostFromUntrustedPeer: a client that reaches the
// listener directly cannot pick the admin plane by sending X-Forwarded-Host.
func TestHostIsolation_ForwardedHostFromUntrustedPeer(t *testing.T) {
	var buf bytes.Buffer
	s := serverWithEnv(t, map[string]string{
		"ARTIFACTSD_BASE_URL":        "https://artifacts.example.com",
		"ARTIFACTSD_ADMIN_HOST":      "admin.example.com",
		"ARTIFACTSD_TRUSTED_PROXIES": "10.0.0.0/8",
	}, &buf)
	gate := s.hostIsolationGate(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	send := func(remote string) int {
		r := httptest.NewRequest(http.MethodGet, "http://artifacts.example.com/admin/", http.NoBody)
		r.Host = "artifacts.example.com"
		r.RemoteAddr = remote
		r.Header.Set("X-Forwarded-Host", "admin.example.com")
		rec := httptest.NewRecorder()
		gate.ServeHTTP(rec, r)
		return rec.Code
	}
	if got := send("198.51.100.1:5000"); got != http.StatusNotFound {
		t.Errorf("direct client with forged X-Forwarded-Host: status %d, want 404", got)
	}
	if got := send("10.0.0.1:5000"); got != http.StatusOK {
		t.Errorf("trusted proxy forwarding the admin host: status %d, want 200", got)
	}
}

func TestLogProxyMode(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want []string
	}{
		{"none", nil, []string{"forwarded headers ignored"}},
		{"explicit", map[string]string{"ARTIFACTSD_TRUSTED_PROXIES": "10.0.0.1"}, []string{"10.0.0.1/32"}},
		{"deprecated", map[string]string{"ARTIFACTSD_TRUST_PROXY": "true"}, []string{"deprecated", "192.168.0.0/16"}},
		{"both", map[string]string{"ARTIFACTSD_TRUST_PROXY": "true", "ARTIFACTSD_TRUSTED_PROXIES": "10.0.0.1"}, []string{"ignored because --trusted-proxies is set", "10.0.0.1/32"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var buf bytes.Buffer
			s := serverWithEnv(t, c.env, &buf)
			var logBuf bytes.Buffer
			s.log = audit.NewAppLogger(&logBuf, "info")
			s.logProxyMode()
			for _, w := range c.want {
				if !strings.Contains(logBuf.String(), w) {
					t.Errorf("log %q missing %q", logBuf.String(), w)
				}
			}
		})
	}
}
