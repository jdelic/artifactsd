package auth

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// claimsFromToken extracts subject, email, and the dynamic group claim.
func claimsFromToken(idToken *oidc.IDToken, groupClaim string) (*oidcClaims, error) {
	if groupClaim == "" {
		groupClaim = "groups"
	}
	var raw map[string]json.RawMessage
	if err := idToken.Claims(&raw); err != nil {
		return nil, fmt.Errorf("auth: oidc claims: %w", err)
	}
	cl := &oidcClaims{Subject: idToken.Subject}
	_ = json.Unmarshal(raw["email"], &cl.Email)
	cl.Groups = parseGroups(raw[groupClaim])
	return cl, nil
}

// LoginResult is the verified identity from a completed OIDC login.
type LoginResult struct {
	Subject string
	Email   string
	Groups  []string
}

// OIDCLogin drives the admin browser login: Authorization Code + PKCE against
// the issuer, then ID-token verification (design/14 §2.3/§7).
type OIDCLogin struct {
	oauth2     oauth2.Config
	verifier   *oidc.IDTokenVerifier
	cache      *OIDCProviderCache
	groupClaim string
}

// NewOIDCLogin builds the login flow for an issuer/client, reusing the provider
// cache for discovery. groupClaim defaults to "groups".
func NewOIDCLogin(ctx context.Context, cache *OIDCProviderCache, issuer, clientID, clientSecret, redirectURL, groupClaim string) (*OIDCLogin, error) {
	provider, err := cache.provider(ctx, issuer)
	if err != nil {
		return nil, err
	}
	if groupClaim == "" {
		groupClaim = "groups"
	}
	return &OIDCLogin{
		oauth2: oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  redirectURL,
			Endpoint:     provider.Endpoint(),
			Scopes:       []string{oidc.ScopeOpenID, "email", "profile", "groups"},
		},
		verifier:   provider.Verifier(&oidc.Config{ClientID: clientID}),
		cache:      cache,
		groupClaim: groupClaim,
	}, nil
}

// NewVerifier returns a fresh PKCE code verifier (store it for the callback).
func NewVerifier() string { return oauth2.GenerateVerifier() }

// AuthCodeURL builds the authorization-endpoint URL with the state and a PKCE
// S256 challenge derived from pkceVerifier.
func (l *OIDCLogin) AuthCodeURL(state, pkceVerifier string) string {
	return l.oauth2.AuthCodeURL(state, oauth2.S256ChallengeOption(pkceVerifier))
}

// Exchange completes the flow: swap the code for tokens (with the PKCE
// verifier), verify the ID token, and return the identity.
func (l *OIDCLogin) Exchange(ctx context.Context, code, pkceVerifier string) (*LoginResult, error) {
	cctx := l.cache.clientContext(ctx)
	tok, err := l.oauth2.Exchange(cctx, code, oauth2.VerifierOption(pkceVerifier))
	if err != nil {
		return nil, fmt.Errorf("auth: oidc code exchange: %w", err)
	}
	rawID, ok := tok.Extra("id_token").(string)
	if !ok || rawID == "" {
		return nil, fmt.Errorf("auth: oidc response missing id_token")
	}
	idToken, err := l.verifier.Verify(cctx, rawID)
	if err != nil {
		return nil, fmt.Errorf("auth: oidc id_token verify: %w", err)
	}
	cl, err := claimsFromToken(idToken, l.groupClaim)
	if err != nil {
		return nil, err
	}
	return &LoginResult{Subject: cl.Subject, Email: cl.Email, Groups: cl.Groups}, nil
}
