package policy

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jdelic/artifactsd/internal/auth"
	"github.com/jdelic/artifactsd/internal/meta"
)

func req(headers map[string]string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/c/demo/x", http.NoBody)
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	return r
}

func effective(vis string, auth []meta.AuthEntry) *meta.Effective {
	return &meta.Effective{Visibility: vis, Auth: auth}
}

func TestDecide_PublicNoAuthServes(t *testing.T) {
	d := Decide(Input{Effective: effective(meta.VisibilityPublic, nil), Request: req(nil)})
	if d.Outcome != Serve {
		t.Errorf("public object outcome = %v, want Serve", d.Outcome)
	}
}

func TestDecide_PrivateIs404(t *testing.T) {
	d := Decide(Input{Effective: effective(meta.VisibilityPrivate, nil), Request: req(nil)})
	if d.Outcome != NotFound {
		t.Errorf("private outcome = %v, want NotFound", d.Outcome)
	}
}

func TestDecide_AncestorPrivateIs404(t *testing.T) {
	d := Decide(Input{Effective: effective(meta.VisibilityPublic, nil), AncestorPrivate: true, Request: req(nil)})
	if d.Outcome != NotFound {
		t.Errorf("ancestor-private outcome = %v, want NotFound", d.Outcome)
	}
}

func TestDecide_ExpiredIs404(t *testing.T) {
	d := Decide(Input{Effective: effective(meta.VisibilityPublic, nil), Expired: true, Request: req(nil)})
	if d.Outcome != NotFound {
		t.Errorf("expired outcome = %v, want NotFound", d.Outcome)
	}
}

func TestDecide_BearerAuthSucceeds(t *testing.T) {
	entries := []meta.AuthEntry{{Type: meta.AuthBearer, TokenHash: auth.HashToken("s3cret")}}
	d := Decide(Input{
		Effective: effective(meta.VisibilityProtected, entries),
		Request:   req(map[string]string{"Authorization": "Bearer s3cret"}),
	})
	if d.Outcome != Serve {
		t.Fatalf("valid bearer outcome = %v, want Serve", d.Outcome)
	}
	if d.Scheme != "Bearer" || d.Subject != auth.TokenPrefix("s3cret") {
		t.Errorf("audit fields = scheme:%q subject:%q", d.Scheme, d.Subject)
	}
}

func TestDecide_BearerOnlyHidesAs404(t *testing.T) {
	entries := []meta.AuthEntry{{Type: meta.AuthBearer, TokenHash: auth.HashToken("right")}}
	// No credentials and wrong credentials both yield 404 (capability-style).
	for _, authz := range []string{"", "Bearer wrong"} {
		h := map[string]string{}
		if authz != "" {
			h["Authorization"] = authz
		}
		d := Decide(Input{Effective: effective(meta.VisibilityProtected, entries), Request: req(h)})
		if d.Outcome != NotFound {
			t.Errorf("bearer-only with %q: outcome = %v, want NotFound", authz, d.Outcome)
		}
	}
}

func TestDecide_BasicChallenges401(t *testing.T) {
	pw, _ := auth.HashPassword("pw")
	entries := []meta.AuthEntry{{Type: meta.AuthBasic, Username: "u", PasswordHash: pw}}

	// No creds → 401 Basic.
	d := Decide(Input{Effective: effective(meta.VisibilityProtected, entries), Request: req(nil)})
	if d.Outcome != Unauthorized || d.Challenge != `Basic realm="artifactsd"` {
		t.Errorf("basic no-creds = %v / %q, want 401 Basic", d.Outcome, d.Challenge)
	}

	// Valid creds → Serve.
	r := req(nil)
	r.SetBasicAuth("u", "pw")
	d = Decide(Input{Effective: effective(meta.VisibilityProtected, entries), Request: r})
	if d.Outcome != Serve || d.Subject != "u" {
		t.Errorf("basic valid = %v subj=%q, want Serve/u", d.Outcome, d.Subject)
	}
}

func TestDecide_OIDCBrowserRedirectsAPIGets401(t *testing.T) {
	entries := []meta.AuthEntry{{Type: meta.AuthOIDC, Issuer: "https://idp", AnyAuthenticated: true}}

	// Browser → 302 redirect.
	d := Decide(Input{Effective: effective(meta.VisibilityProtected, entries), Request: req(map[string]string{"Accept": "text/html"})})
	if d.Outcome != RedirectOIDC {
		t.Errorf("oidc browser outcome = %v, want RedirectOIDC", d.Outcome)
	}

	// API client → 401 Bearer.
	d = Decide(Input{Effective: effective(meta.VisibilityProtected, entries), Request: req(map[string]string{"Accept": "application/json"})})
	if d.Outcome != Unauthorized || d.Challenge != "Bearer" {
		t.Errorf("oidc api = %v / %q, want 401 Bearer", d.Outcome, d.Challenge)
	}
}

func TestDecide_ChallengePrecedenceOIDCOverBasicOverBearer(t *testing.T) {
	pw, _ := auth.HashPassword("pw")
	entries := []meta.AuthEntry{
		{Type: meta.AuthBearer, TokenHash: "x"},
		{Type: meta.AuthBasic, Username: "u", PasswordHash: pw},
		{Type: meta.AuthOIDC, Issuer: "https://idp", AnyAuthenticated: true},
	}
	// Browser, unauthenticated → OIDC wins (redirect).
	d := Decide(Input{Effective: effective(meta.VisibilityProtected, entries), Request: req(map[string]string{"Accept": "text/html"})})
	if d.Outcome != RedirectOIDC {
		t.Errorf("precedence: outcome = %v, want RedirectOIDC (oidc precedence)", d.Outcome)
	}
}

func TestDecide_DirectoryListingRule(t *testing.T) {
	// Directory request with listing off → 404.
	eff := &meta.Effective{Visibility: meta.VisibilityPublic, DirectoryListing: false}
	d := Decide(Input{Effective: eff, IsDirectory: true, Request: req(nil)})
	if d.Outcome != NotFound {
		t.Errorf("dir listing off outcome = %v, want NotFound", d.Outcome)
	}

	// Directory request with listing on → Serve.
	eff.DirectoryListing = true
	d = Decide(Input{Effective: eff, IsDirectory: true, Request: req(nil)})
	if d.Outcome != Serve {
		t.Errorf("dir listing on outcome = %v, want Serve", d.Outcome)
	}

	// File request is unaffected by listing being off.
	eff.DirectoryListing = false
	d = Decide(Input{Effective: eff, IsDirectory: false, Request: req(nil)})
	if d.Outcome != Serve {
		t.Errorf("file with listing off outcome = %v, want Serve", d.Outcome)
	}
}

func TestDecide_UnlistedServesByURL(t *testing.T) {
	d := Decide(Input{Effective: effective(meta.VisibilityUnlisted, nil), Request: req(nil)})
	if d.Outcome != Serve {
		t.Errorf("unlisted outcome = %v, want Serve", d.Outcome)
	}
}
