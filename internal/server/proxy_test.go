package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSecureAndScheme(t *testing.T) {
	cases := []struct {
		name       string
		trustProxy bool
		xfProto    string
		wantSecure bool
	}{
		{"plain http no proxy", false, "", false},
		{"xfp https but untrusted", false, "https", false},
		{"xfp https trusted", true, "https", true},
		{"xfp http trusted", true, "http", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var buf bytes.Buffer
			env := map[string]string{}
			if c.trustProxy {
				env["ARTIFACTSD_TRUST_PROXY"] = "true"
			}
			s := serverWithEnv(t, env, &buf)
			r := httptest.NewRequest(http.MethodGet, "/x", http.NoBody)
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

func TestClientIP_TrustProxy(t *testing.T) {
	var buf bytes.Buffer
	s := serverWithEnv(t, map[string]string{"ARTIFACTSD_TRUST_PROXY": "true"}, &buf)
	r := httptest.NewRequest(http.MethodGet, "/x", http.NoBody)
	r.RemoteAddr = "10.0.0.1:5000"
	r.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.1")
	if got := s.clientIP(r); got != "203.0.113.9" {
		t.Errorf("clientIP with trust_proxy = %q, want 203.0.113.9", got)
	}

	// Without trust_proxy, the header is ignored.
	s2 := serverWithEnv(t, nil, &buf)
	if got := s2.clientIP(r); got != "10.0.0.1" {
		t.Errorf("clientIP without trust_proxy = %q, want 10.0.0.1", got)
	}
}
