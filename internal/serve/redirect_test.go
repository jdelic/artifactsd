package serve

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestRedirectToSlash pins the trailing-slash redirect target and that a path
// starting with "//" is never turned into a protocol-relative redirect.
func TestRedirectToSlash(t *testing.T) {
	for _, tc := range []struct {
		target   string
		redirect bool
		location string
	}{
		{"/c/docs/folder", true, "/c/docs/folder/"},
		{"/c/docs/folder?x=1", true, "/c/docs/folder/?x=1"},
		{"/c/docs/folder/", false, ""},
		{"//evil.example/x", false, ""},
	} {
		r := httptest.NewRequest(http.MethodGet, "http://artifacts.example"+tc.target, http.NoBody)
		w := httptest.NewRecorder()
		if got := redirectToSlash(w, r); got != tc.redirect {
			t.Errorf("%s: redirected = %v, want %v", tc.target, got, tc.redirect)
			continue
		}
		if loc := w.Header().Get("Location"); loc != tc.location {
			t.Errorf("%s: Location = %q, want %q", tc.target, loc, tc.location)
		}
	}
}
