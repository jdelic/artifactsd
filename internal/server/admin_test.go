package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jdelic/artifactsd/internal/auth"
	"github.com/jdelic/artifactsd/internal/session"
)

// adminSession mints a signed admin session cookie for the given groups.
func adminSession(t *testing.T, s *Server, groups []string) *http.Cookie {
	t.Helper()
	rec := httptest.NewRecorder()
	if err := s.session.SetCookie(rec, &session.Identity{Subject: "u1", Groups: groups, Expiry: time.Now().Add(time.Hour)}); err != nil {
		t.Fatalf("SetCookie: %v", err)
	}
	return rec.Result().Cookies()[0]
}

func gatedReq(cookie *http.Cookie, accept string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/admin/", http.NoBody)
	if cookie != nil {
		r.AddCookie(cookie)
	}
	if accept != "" {
		r.Header.Set("Accept", accept)
	}
	return r
}

func TestAdminGate_AllowsAdminGroup(t *testing.T) {
	var buf bytes.Buffer
	s := serverWithEnv(t, map[string]string{
		"ARTIFACTSD_OIDC_ISSUER":       "https://idp.example.com",
		"ARTIFACTSD_OIDC_CLIENT_ID":    "artifactsd",
		"ARTIFACTSD_OIDC_REDIRECT_URL": "https://x.example.com/auth/oidc/callback",
		"ARTIFACTSD_ADMIN_GROUP":       "admins",
	}, &buf)

	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, gatedReq(adminSession(t, s, []string{"admins"}), "text/html"))
	if rec.Code != http.StatusNotImplemented { // passes the gate, hits the stub
		t.Errorf("admin in group = %d, want 501 (passed gate)", rec.Code)
	}
}

func TestAdminGate_DeniesNonAdminGroup(t *testing.T) {
	var buf bytes.Buffer
	s := serverWithEnv(t, map[string]string{
		"ARTIFACTSD_OIDC_ISSUER":       "https://idp.example.com",
		"ARTIFACTSD_OIDC_CLIENT_ID":    "artifactsd",
		"ARTIFACTSD_OIDC_REDIRECT_URL": "https://x.example.com/auth/oidc/callback",
		"ARTIFACTSD_ADMIN_GROUP":       "admins",
	}, &buf)

	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, gatedReq(adminSession(t, s, []string{"users"}), "text/html"))
	// Not an admin → browser redirected to OIDC login.
	if rec.Code != http.StatusFound {
		t.Errorf("non-admin browser = %d, want 302 redirect", rec.Code)
	}
	if loc := rec.Header().Get("Location"); !strings.HasPrefix(loc, "/auth/oidc/login") {
		t.Errorf("redirect Location = %q, want /auth/oidc/login", loc)
	}
}

func TestAdminGate_APIClientGets401(t *testing.T) {
	var buf bytes.Buffer
	s := serverWithEnv(t, map[string]string{
		"ARTIFACTSD_OIDC_ISSUER":       "https://idp.example.com",
		"ARTIFACTSD_OIDC_CLIENT_ID":    "artifactsd",
		"ARTIFACTSD_OIDC_REDIRECT_URL": "https://x.example.com/auth/oidc/callback",
		"ARTIFACTSD_ADMIN_GROUP":       "admins",
	}, &buf)

	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, gatedReq(nil, "application/json"))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated API = %d, want 401", rec.Code)
	}
}

func TestAdminGate_BasicFallback(t *testing.T) {
	var buf bytes.Buffer
	hash, _ := auth.HashPassword("s3cret")
	s := serverWithEnv(t, map[string]string{
		"ARTIFACTSD_ADMIN_BASIC_USER":          "root",
		"ARTIFACTSD_ADMIN_BASIC_PASSWORD_HASH": hash,
	}, &buf)

	// Correct basic creds pass the gate.
	r := gatedReq(nil, "")
	r.SetBasicAuth("root", "s3cret")
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, r)
	if rec.Code != http.StatusNotImplemented {
		t.Errorf("valid basic admin = %d, want 501 (passed gate)", rec.Code)
	}

	// Wrong creds → 401 with Basic challenge.
	r = gatedReq(nil, "")
	r.SetBasicAuth("root", "wrong")
	rec = httptest.NewRecorder()
	s.routes().ServeHTTP(rec, r)
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Header().Get("WWW-Authenticate"), "Basic") {
		t.Errorf("wrong basic = %d / %q, want 401 Basic", rec.Code, rec.Header().Get("WWW-Authenticate"))
	}
}

func TestSanitizeReturnTo(t *testing.T) {
	cases := map[string]string{
		"/admin/collections": "/admin/collections",
		"//evil.com":         "/admin/",
		"https://evil.com":   "/admin/",
		"/other":             "/admin/",
		"":                   "/admin/",
	}
	for in, want := range cases {
		if got := sanitizeReturnTo(in, "/admin"); got != want {
			t.Errorf("sanitizeReturnTo(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHandleOIDCLogin_Unconfigured503(t *testing.T) {
	var buf bytes.Buffer
	s := serverWithEnv(t, nil, &buf) // no OIDC configured
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/auth/oidc/login", http.NoBody))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("login without OIDC = %d, want 503", rec.Code)
	}
}
