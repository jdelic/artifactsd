package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestHostIsolationGate covers the separate-admin-origin topology
// (design/14 §10, artifactsd-1it): the admin control plane is reachable only on
// the admin host, the public artifact plane only on the public host, and shared
// cookie-less routes on both.
func TestHostIsolationGate(t *testing.T) {
	var buf bytes.Buffer
	s := serverWithEnv(t, map[string]string{
		"ARTIFACTSD_BASE_URL":   "https://artifacts.example.com",
		"ARTIFACTSD_ADMIN_HOST": "admin.example.com",
	}, &buf)

	// A sentinel downstream handler: if the gate lets a request through, we see 200.
	gate := s.hostIsolationGate(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	const (
		publicHost = "artifacts.example.com"
		adminHost  = "admin.example.com"
	)
	cases := []struct {
		name string
		host string
		path string
		want int
	}{
		{"admin UI on admin host", adminHost, "/admin/", http.StatusOK},
		{"admin UI on public host blocked", publicHost, "/admin/", http.StatusNotFound},
		{"oidc callback on admin host", adminHost, "/auth/oidc/callback", http.StatusOK},
		{"oidc callback on public host blocked", publicHost, "/auth/oidc/callback", http.StatusNotFound},
		{"artifact on public host", publicHost, "/c/reports/index.html", http.StatusOK},
		{"artifact on admin host blocked", adminHost, "/c/reports/index.html", http.StatusNotFound},
		{"share on public host", publicHost, "/s/abc/", http.StatusOK},
		{"share on admin host blocked", adminHost, "/s/abc/", http.StatusNotFound},
		{"robots on public host", publicHost, "/robots.txt", http.StatusOK},
		{"robots on admin host blocked", adminHost, "/robots.txt", http.StatusNotFound},
		// Shared cookie-less routes are reachable on either host.
		{"management API on admin host", adminHost, "/api/v1/collections", http.StatusOK},
		{"management API on public host", publicHost, "/api/v1/collections", http.StatusOK},
		{"healthz on admin host", adminHost, "/healthz", http.StatusOK},
		{"root on public host", publicHost, "/", http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "http://"+tc.host+tc.path, http.NoBody)
			r.Host = tc.host
			rec := httptest.NewRecorder()
			gate.ServeHTTP(rec, r)
			if rec.Code != tc.want {
				t.Errorf("%s %s on %s = %d, want %d", r.Method, tc.path, tc.host, rec.Code, tc.want)
			}
		})
	}
}

// TestHostIsolationGate_SingleDomainNoop verifies the gate is transparent when
// ARTIFACTSD_ADMIN_HOST is unset: every route is served on the one origin.
func TestHostIsolationGate_SingleDomainNoop(t *testing.T) {
	var buf bytes.Buffer
	s := serverWithEnv(t, nil, &buf) // base URL only, no admin host
	gate := s.hostIsolationGate(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	for _, path := range []string{"/admin/", "/auth/oidc/callback", "/c/x/y", "/robots.txt", "/api/v1/collections"} {
		r := httptest.NewRequest(http.MethodGet, "http://x.example.com"+path, http.NoBody)
		rec := httptest.NewRecorder()
		gate.ServeHTTP(rec, r)
		if rec.Code != http.StatusOK {
			t.Errorf("single-domain gate blocked %s (%d), want pass-through", path, rec.Code)
		}
	}
}

// TestSameOrigin_AdminHost confirms admin form posts whose Origin is the admin
// host pass the CSRF same-origin check in the separate-admin-origin topology.
func TestSameOrigin_AdminHost(t *testing.T) {
	var buf bytes.Buffer
	s := serverWithEnv(t, map[string]string{
		"ARTIFACTSD_BASE_URL":   "https://artifacts.example.com",
		"ARTIFACTSD_ADMIN_HOST": "admin.example.com",
	}, &buf)

	cases := []struct {
		origin string
		want   bool
	}{
		{"https://admin.example.com", true},     // the admin origin
		{"https://artifacts.example.com", true}, // the public base origin
		{"https://evil.example.com", false},     // anything else
		{"", true},                              // absent Origin defers to the token check
	}
	for _, tc := range cases {
		r := httptest.NewRequest(http.MethodPost, "https://admin.example.com/admin/logout", http.NoBody)
		if tc.origin != "" {
			r.Header.Set("Origin", tc.origin)
		}
		if got := s.sameOrigin(r); got != tc.want {
			t.Errorf("sameOrigin(Origin=%q) = %v, want %v", tc.origin, got, tc.want)
		}
	}
}
