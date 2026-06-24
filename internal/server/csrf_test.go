package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func csrfTestServer(t *testing.T) (*Server, *http.Cookie) {
	t.Helper()
	var buf bytes.Buffer
	s := serverWithEnv(t, map[string]string{
		"ARTIFACTSD_OIDC_ISSUER":       "https://idp.example.com",
		"ARTIFACTSD_OIDC_CLIENT_ID":    "artifactsd",
		"ARTIFACTSD_OIDC_REDIRECT_URL": "https://x.example.com/auth/oidc/callback",
		"ARTIFACTSD_ADMIN_GROUP":       "admins",
	}, &buf)
	return s, adminSession(t, s, []string{"admins"})
}

func csrfOK(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }

// csrfTokenFor derives the CSRF token for the session carried by cookie.
func csrfTokenFor(t *testing.T, s *Server, cookie *http.Cookie) string {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/admin/", http.NoBody)
	r.AddCookie(cookie)
	id, err := s.session.FromRequest(r)
	if err != nil {
		t.Fatalf("FromRequest: %v", err)
	}
	return s.session.CSRFToken(id)
}

func TestCSRFGate_AllowsSafeMethodsWithoutToken(t *testing.T) {
	s, cookie := csrfTestServer(t)
	r := httptest.NewRequest(http.MethodGet, "/admin/", http.NoBody)
	r.AddCookie(cookie)
	rec := httptest.NewRecorder()
	s.csrfGate(http.HandlerFunc(csrfOK)).ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Errorf("GET without token = %d, want 200", rec.Code)
	}
}

func TestCSRFGate_AllowsValidToken(t *testing.T) {
	s, cookie := csrfTestServer(t)
	token := csrfTokenFor(t, s, cookie)

	r := httptest.NewRequest(http.MethodPost, "/admin/maintenance/cleanup", http.NoBody)
	r.AddCookie(cookie)
	r.Header.Set("X-CSRF-Token", token)
	rec := httptest.NewRecorder()
	s.csrfGate(http.HandlerFunc(csrfOK)).ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Errorf("valid token = %d, want 200", rec.Code)
	}
}

func TestCSRFGate_RejectsMissingToken(t *testing.T) {
	s, cookie := csrfTestServer(t)
	r := httptest.NewRequest(http.MethodPost, "/admin/maintenance/cleanup", http.NoBody)
	r.AddCookie(cookie)
	rec := httptest.NewRecorder()
	s.csrfGate(http.HandlerFunc(csrfOK)).ServeHTTP(rec, r)
	if rec.Code != http.StatusForbidden {
		t.Errorf("missing token = %d, want 403", rec.Code)
	}
}

func TestCSRFGate_RejectsWrongToken(t *testing.T) {
	s, cookie := csrfTestServer(t)
	r := httptest.NewRequest(http.MethodPost, "/admin/maintenance/cleanup", http.NoBody)
	r.AddCookie(cookie)
	r.Header.Set("X-CSRF-Token", "garbage")
	rec := httptest.NewRecorder()
	s.csrfGate(http.HandlerFunc(csrfOK)).ServeHTTP(rec, r)
	if rec.Code != http.StatusForbidden {
		t.Errorf("wrong token = %d, want 403", rec.Code)
	}
}

func TestCSRFGate_RejectsNoSession(t *testing.T) {
	s, _ := csrfTestServer(t)
	r := httptest.NewRequest(http.MethodPost, "/admin/maintenance/cleanup", http.NoBody)
	rec := httptest.NewRecorder()
	s.csrfGate(http.HandlerFunc(csrfOK)).ServeHTTP(rec, r)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("no session = %d, want 401", rec.Code)
	}
}

func TestCSRFGate_RejectsCrossOrigin(t *testing.T) {
	s, cookie := csrfTestServer(t)
	token := csrfTokenFor(t, s, cookie)

	r := httptest.NewRequest(http.MethodPost, "/admin/maintenance/cleanup", http.NoBody)
	r.AddCookie(cookie)
	r.Header.Set("X-CSRF-Token", token)
	r.Header.Set("Origin", "https://evil.example.com")
	rec := httptest.NewRecorder()
	s.csrfGate(http.HandlerFunc(csrfOK)).ServeHTTP(rec, r)
	if rec.Code != http.StatusForbidden {
		t.Errorf("cross-origin = %d, want 403", rec.Code)
	}
}

func TestCSRFGate_AllowsSameOrigin(t *testing.T) {
	s, cookie := csrfTestServer(t)
	token := csrfTokenFor(t, s, cookie)

	r := httptest.NewRequest(http.MethodPost, "/admin/maintenance/cleanup", http.NoBody)
	r.AddCookie(cookie)
	r.Header.Set("X-CSRF-Token", token)
	r.Header.Set("Origin", "https://x.example.com")
	rec := httptest.NewRecorder()
	s.csrfGate(http.HandlerFunc(csrfOK)).ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Errorf("same-origin = %d, want 200", rec.Code)
	}
}

func TestCSRFGate_AllowsFormFieldToken(t *testing.T) {
	s, cookie := csrfTestServer(t)
	token := csrfTokenFor(t, s, cookie)

	r := httptest.NewRequest(http.MethodPost, "/admin/maintenance/cleanup",
		bytes.NewBufferString("csrf_token="+token))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(cookie)
	rec := httptest.NewRecorder()
	s.csrfGate(http.HandlerFunc(csrfOK)).ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Errorf("form field token = %d, want 200", rec.Code)
	}
}
