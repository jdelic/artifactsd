package auth

import (
	"context"
	"net/url"
	"strings"
	"testing"
)

func TestOIDCLogin_AuthCodeURL(t *testing.T) {
	mi := newMockIssuer(t)
	cache := NewOIDCProviderCache(mi.server.Client())

	login, err := NewOIDCLogin(context.Background(), cache,
		mi.url, "artifactsd", "secret", "https://x.example.com/auth/oidc/callback", "groups", nil)
	if err != nil {
		t.Fatalf("NewOIDCLogin: %v", err)
	}

	verifier := NewVerifier()
	raw := login.AuthCodeURL("state-123", verifier)
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse auth url: %v", err)
	}
	q := u.Query()
	checks := map[string]string{
		"client_id":             "artifactsd",
		"response_type":         "code",
		"redirect_uri":          "https://x.example.com/auth/oidc/callback",
		"state":                 "state-123",
		"code_challenge_method": "S256",
	}
	for k, want := range checks {
		if q.Get(k) != want {
			t.Errorf("auth URL %s = %q, want %q", k, q.Get(k), want)
		}
	}
	if q.Get("code_challenge") == "" {
		t.Error("auth URL missing PKCE code_challenge")
	}
	if !strings.Contains(q.Get("scope"), "openid") {
		t.Errorf("scope = %q, want openid", q.Get("scope"))
	}
	// The authorization endpoint should be the issuer's.
	if !strings.HasPrefix(raw, mi.url) {
		t.Errorf("auth URL %q does not target the issuer", raw)
	}
}

// TestOIDCLogin_DefaultScopesDropGroups verifies the default scope set is the
// standard "openid email profile" and that the non-standard "groups" scope is
// no longer requested (artifactsd-7gx).
func TestOIDCLogin_DefaultScopesDropGroups(t *testing.T) {
	mi := newMockIssuer(t)
	cache := NewOIDCProviderCache(mi.server.Client())

	login, err := NewOIDCLogin(context.Background(), cache,
		mi.url, "artifactsd", "secret", "https://x.example.com/auth/oidc/callback", "groups", nil)
	if err != nil {
		t.Fatalf("NewOIDCLogin: %v", err)
	}
	u, err := url.Parse(login.AuthCodeURL("s", NewVerifier()))
	if err != nil {
		t.Fatalf("parse auth url: %v", err)
	}
	scope := u.Query().Get("scope")
	for _, want := range []string{"openid", "email", "profile"} {
		if !strings.Contains(scope, want) {
			t.Errorf("default scope %q missing %q", scope, want)
		}
	}
	if strings.Contains(scope, "groups") {
		t.Errorf("default scope %q should not request the non-standard groups scope", scope)
	}
}

// TestResolveScopes covers defaulting and the always-present openid guarantee.
func TestResolveScopes(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"nil defaults", nil, DefaultOIDCScopes},
		{"empty defaults", []string{}, DefaultOIDCScopes},
		{"custom keeps openid", []string{"openid", "groups"}, []string{"openid", "groups"}},
		{"openid prepended when missing", []string{"email", "groups"}, []string{"openid", "email", "groups"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveScopes(tc.in)
			if strings.Join(got, " ") != strings.Join(tc.want, " ") {
				t.Errorf("resolveScopes(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestNewVerifier_Unique(t *testing.T) {
	a, b := NewVerifier(), NewVerifier()
	if a == b {
		t.Error("PKCE verifiers should be unique")
	}
	if len(a) < 43 {
		t.Error("PKCE verifier too short")
	}
}
