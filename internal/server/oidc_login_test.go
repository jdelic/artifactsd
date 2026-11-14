package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jdelic/artifactsd/internal/auth"
	"github.com/jdelic/artifactsd/internal/session"
)

func hasCookie(rec *httptest.ResponseRecorder, name string) (*http.Cookie, bool) {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c, true
		}
	}
	return nil, false
}

// TestCompleteLogin_NonAdminGets403: a user the IdP authenticates but who is
// not an admin gets a 403 explanation and no session cookie, instead of an
// endless redirect loop (artifactsd-m9e.16).
func TestCompleteLogin_NonAdminGets403(t *testing.T) {
	s := serverWithStore(t) // admin group "admins"
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/auth/oidc/callback", http.NoBody)
	s.completeLogin(rec, r, &flowState{ReturnTo: "/admin/"}, &auth.LoginResult{
		Subject: "sub-1", Email: "mallory@example.com", Groups: []string{"users"},
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin callback = %d, want 403", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Signed in as mallory@example.com") ||
		!strings.Contains(rec.Body.String(), "not authorized for the admin UI") {
		t.Errorf("body = %q", rec.Body.String())
	}
	if c, ok := hasCookie(rec, session.DefaultCookieName); ok && c.MaxAge >= 0 && c.Value != "" {
		t.Error("admin session cookie set for a non-admin")
	}
	if c, ok := hasCookie(rec, flowCookieName); !ok || c.MaxAge >= 0 {
		t.Error("flow cookie not cleared")
	}
	assertAudit(t, s, "admin.login_denied", "mallory@example.com")
}

func TestCompleteLogin_AdminGetsSession(t *testing.T) {
	s := serverWithStore(t)
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/auth/oidc/callback", http.NoBody)
	s.completeLogin(rec, r, &flowState{ReturnTo: "/admin/collections"}, &auth.LoginResult{
		Subject: "sub-2", Email: "alice@example.com", Groups: []string{"admins"},
	})
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/admin/collections" {
		t.Fatalf("admin callback = %d to %q, want 302 to /admin/collections", rec.Code, rec.Header().Get("Location"))
	}
	if c, ok := hasCookie(rec, session.DefaultCookieName); !ok || c.Value == "" {
		t.Error("no admin session cookie")
	}
	assertAudit(t, s, "admin.login", "alice@example.com")
}

// TestAdminGate_ValidSessionNoLongerAdmin: a valid session for an identity
// that is no longer authorized (config changed) gets 403 and loses the cookie,
// not a redirect to the IdP.
func TestAdminGate_ValidSessionNoLongerAdmin(t *testing.T) {
	s := serverWithStore(t)
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, gatedReq(adminSession(t, s, []string{"users"}), "text/html"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("valid non-admin session = %d, want 403", rec.Code)
	}
	if c, ok := hasCookie(rec, session.DefaultCookieName); !ok || c.MaxAge >= 0 {
		t.Error("session cookie not cleared")
	}
}

func assertAudit(t *testing.T, s *Server, action, actorPart string) {
	t.Helper()
	entries, err := s.store.ListAudit(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Action == action && e.Actor != nil && strings.Contains(*e.Actor, actorPart) {
			return
		}
	}
	t.Errorf("no audit entry %s for %s in %+v", action, actorPart, entries)
}
