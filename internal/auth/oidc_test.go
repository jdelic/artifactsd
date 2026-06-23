package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
)

// mockIssuer is a minimal OIDC provider for tests: discovery + JWKS + signing.
type mockIssuer struct {
	server *httptest.Server
	key    *rsa.PrivateKey
	kid    string
	url    string
}

func newMockIssuer(t *testing.T) *mockIssuer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	mi := &mockIssuer{key: key, kid: "test-kid"}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                 mi.url,
			"jwks_uri":               mi.url + "/jwks",
			"authorization_endpoint": mi.url + "/authorize",
			"token_endpoint":         mi.url + "/token",
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		set := jose.JSONWebKeySet{Keys: []jose.JSONWebKey{
			{Key: &key.PublicKey, KeyID: mi.kid, Algorithm: "RS256", Use: "sig"},
		}}
		_ = json.NewEncoder(w).Encode(set)
	})
	mi.server = httptest.NewServer(mux)
	mi.url = mi.server.URL
	t.Cleanup(mi.server.Close)
	return mi
}

// sign issues a signed JWT with the given claims, using the issuer's key (or an
// alternate key to simulate a bad signature).
func (mi *mockIssuer) sign(t *testing.T, claims map[string]any, key *rsa.PrivateKey) string {
	t.Helper()
	if key == nil {
		key = mi.key
	}
	opts := (&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", mi.kid)
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, opts)
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	payload, _ := json.Marshal(claims)
	obj, err := signer.Sign(payload)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	raw, err := obj.CompactSerialize()
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}
	return raw
}

func (mi *mockIssuer) baseClaims(aud string) map[string]any {
	return map[string]any{
		"iss": mi.url,
		"sub": "user-123",
		"aud": aud,
		"exp": time.Now().Add(time.Hour).Unix(),
		"iat": time.Now().Add(-time.Minute).Unix(),
	}
}

func oidcReq(token string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/c/demo/x", http.NoBody)
	r.Header.Set("Authorization", "Bearer "+token)
	return r
}

func TestOIDCVerifier_AnyAuthenticated(t *testing.T) {
	mi := newMockIssuer(t)
	cache := NewOIDCProviderCache(mi.server.Client())
	v := NewOIDCVerifier(&OIDCConfig{Issuer: mi.url, Audience: "artifactsd", AnyAuthenticated: true}, cache)

	claims := mi.baseClaims("artifactsd")
	claims["email"] = "alice@example.com"
	ok, subject := v.Verify(oidcReq(mi.sign(t, claims, nil)))
	if !ok {
		t.Fatal("valid token with any_authenticated should be allowed")
	}
	if subject != "alice@example.com" {
		t.Errorf("subject = %q, want email", subject)
	}
}

func TestOIDCVerifier_GroupWhitelist(t *testing.T) {
	mi := newMockIssuer(t)
	cache := NewOIDCProviderCache(mi.server.Client())
	v := NewOIDCVerifier(&OIDCConfig{Issuer: mi.url, Audience: "artifactsd", AllowedGroups: []string{"staff"}}, cache)

	claims := mi.baseClaims("artifactsd")
	claims["groups"] = []string{"eng", "staff"}
	if ok, _ := v.Verify(oidcReq(mi.sign(t, claims, nil))); !ok {
		t.Error("token in allowed group should be allowed")
	}

	// Not in the allowed group → denied.
	claims["groups"] = []string{"interns"}
	if ok, _ := v.Verify(oidcReq(mi.sign(t, claims, nil))); ok {
		t.Error("token not in allowed group should be denied")
	}
}

func TestOIDCVerifier_SubjectWhitelist(t *testing.T) {
	mi := newMockIssuer(t)
	cache := NewOIDCProviderCache(mi.server.Client())
	v := NewOIDCVerifier(&OIDCConfig{Issuer: mi.url, Audience: "artifactsd", AllowedSubjects: []string{"alice@example.com"}}, cache)

	claims := mi.baseClaims("artifactsd")
	claims["email"] = "alice@example.com"
	if ok, _ := v.Verify(oidcReq(mi.sign(t, claims, nil))); !ok {
		t.Error("whitelisted subject should be allowed")
	}

	claims["email"] = "mallory@example.com"
	if ok, _ := v.Verify(oidcReq(mi.sign(t, claims, nil))); ok {
		t.Error("non-whitelisted subject should be denied")
	}
}

func TestOIDCVerifier_RejectsBadToken(t *testing.T) {
	mi := newMockIssuer(t)
	cache := NewOIDCProviderCache(mi.server.Client())
	v := NewOIDCVerifier(&OIDCConfig{Issuer: mi.url, Audience: "artifactsd", AnyAuthenticated: true}, cache)

	t.Run("wrong audience", func(t *testing.T) {
		if ok, _ := v.Verify(oidcReq(mi.sign(t, mi.baseClaims("someone-else"), nil))); ok {
			t.Error("token with wrong audience should be rejected")
		}
	})
	t.Run("expired", func(t *testing.T) {
		claims := mi.baseClaims("artifactsd")
		claims["exp"] = time.Now().Add(-time.Hour).Unix()
		if ok, _ := v.Verify(oidcReq(mi.sign(t, claims, nil))); ok {
			t.Error("expired token should be rejected")
		}
	})
	t.Run("bad signature", func(t *testing.T) {
		otherKey, _ := rsa.GenerateKey(rand.Reader, 2048)
		if ok, _ := v.Verify(oidcReq(mi.sign(t, mi.baseClaims("artifactsd"), otherKey))); ok {
			t.Error("token signed by an unknown key should be rejected")
		}
	})
	t.Run("not a bearer", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		if ok, _ := v.Verify(r); ok {
			t.Error("missing token should be rejected")
		}
	})
}

func TestParseGroups(t *testing.T) {
	if g := parseGroups(json.RawMessage(`["a","b"]`)); len(g) != 2 {
		t.Errorf("array groups = %v", g)
	}
	if g := parseGroups(json.RawMessage(`"solo"`)); len(g) != 1 || g[0] != "solo" {
		t.Errorf("string group = %v", g)
	}
	if g := parseGroups(nil); g != nil {
		t.Errorf("nil groups = %v", g)
	}
}
