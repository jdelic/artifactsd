package store

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jdelic/artifactsd/internal/auth"
)

// TestTokenSource_EndToEnd authenticates a real DB-stored token through the
// auth.Authenticator, exercising scopes, last-used tracking, and revocation.
func TestTokenSource_EndToEnd(t *testing.T) {
	s, ctx := newStore(t)

	token, hash, prefix, err := auth.GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	rec := &APIToken{Name: "ci", TokenHash: hash, TokenPrefix: prefix, Scopes: []byte(`["collections:write"]`)}
	if err = s.CreateToken(ctx, rec); err != nil {
		t.Fatalf("CreateToken: %v", err)
	}

	authn := auth.NewAuthenticator(s.AuthTokenSource())

	r := httptest.NewRequest(http.MethodGet, "/api/v1/collections", http.NoBody)
	r.Header.Set("Authorization", "Bearer "+token)

	p, err := authn.Authenticate(ctx, r)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if p.Name != "ci" || !p.Scopes.Satisfies(auth.ScopeCollectionsRead) {
		t.Errorf("principal = %+v", p)
	}

	// last_used_at is now set.
	got, _ := s.GetTokenByHash(ctx, hash)
	if got.LastUsedAt == nil {
		t.Error("last_used_at not updated by Authenticate")
	}

	// Revoke → authentication fails.
	if err := s.RevokeToken(ctx, rec.ID, fixedNow); err != nil {
		t.Fatalf("RevokeToken: %v", err)
	}
	if _, err := authn.Authenticate(ctx, r); !errors.Is(err, auth.ErrTokenRevoked) {
		t.Errorf("after revoke err = %v, want ErrTokenRevoked", err)
	}
}

// TestTokenSource_Unknown maps a missing token to auth.ErrUnknownToken.
func TestTokenSource_Unknown(t *testing.T) {
	s, ctx := newStore(t)
	if _, err := s.AuthTokenSource().LookupToken(ctx, "deadbeef"); !errors.Is(err, auth.ErrUnknownToken) {
		t.Errorf("LookupToken unknown = %v, want ErrUnknownToken", err)
	}
}
