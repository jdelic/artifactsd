package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

// OIDCConfig describes one object-level OIDC auth entry (design/14 §2.3). A
// token is authorized if it satisfies ANY of: sub/email in AllowedSubjects, a
// group in GroupClaim within AllowedGroups, or AnyAuthenticated.
type OIDCConfig struct {
	Issuer          string
	Audience        string
	AllowedSubjects []string
	AllowedGroups   []string
	GroupClaim      string // default "groups"
	// InsecureSkipAudienceCheck disables audience (aud) verification. It is the
	// explicit opt-out for issuers without a usable audience; an empty Audience
	// alone no longer disables the check (artifactsd-bhq).
	InsecureSkipAudienceCheck bool
	AnyAuthenticated          bool
}

func (c *OIDCConfig) groupClaim() string {
	if c.GroupClaim == "" {
		return "groups"
	}
	return c.GroupClaim
}

// OIDCProviderCache memoizes go-oidc providers (issuer discovery + JWKS) so each
// issuer is discovered once. It is safe for concurrent use. An optional
// httpClient overrides the transport (used in tests against a mock issuer).
type OIDCProviderCache struct {
	httpClient *http.Client
	mu         sync.Mutex
	providers  map[string]*oidc.Provider
}

// NewOIDCProviderCache returns an empty cache. Pass a non-nil client to override
// the HTTP transport used for discovery/JWKS.
func NewOIDCProviderCache(client *http.Client) *OIDCProviderCache {
	return &OIDCProviderCache{httpClient: client, providers: map[string]*oidc.Provider{}}
}

// clientContext injects the override HTTP client (if any) for go-oidc.
func (c *OIDCProviderCache) clientContext(ctx context.Context) context.Context {
	if c.httpClient != nil {
		return oidc.ClientContext(ctx, c.httpClient)
	}
	return ctx
}

// provider returns the cached provider for issuer, discovering it on first use.
func (c *OIDCProviderCache) provider(ctx context.Context, issuer string) (*oidc.Provider, error) {
	c.mu.Lock()
	p, ok := c.providers[issuer]
	c.mu.Unlock()
	if ok {
		return p, nil
	}

	dctx, cancel := context.WithTimeout(c.clientContext(ctx), 10*time.Second)
	defer cancel()
	p, err := oidc.NewProvider(dctx, issuer)
	if err != nil {
		return nil, fmt.Errorf("auth: oidc discovery for %q: %w", issuer, err)
	}

	c.mu.Lock()
	c.providers[issuer] = p
	c.mu.Unlock()
	return p, nil
}

// claims holds the standard claims plus a dynamic group claim. Groups can be a
// string or array, so it is captured raw and parsed leniently.
type oidcClaims struct {
	Subject string
	Email   string
	Groups  []string
}

// verifyToken discovers (or reuses) the issuer, verifies the token's signature/
// exp/aud, and returns its claims.
func (c *OIDCProviderCache) verifyToken(ctx context.Context, cfg *OIDCConfig, rawToken string) (*oidcClaims, error) {
	provider, err := c.provider(ctx, cfg.Issuer)
	if err != nil {
		return nil, err
	}
	oc := &oidc.Config{ClientID: cfg.Audience}
	// Only skip the aud check when the operator explicitly opted out. An empty
	// audience without that flag would otherwise silently accept any client's
	// token from this issuer (artifactsd-bhq); metadata validation rejects that
	// combination, and this is the matching defense at verification time.
	if cfg.InsecureSkipAudienceCheck {
		oc.SkipClientIDCheck = true
	}
	idToken, err := provider.Verifier(oc).Verify(c.clientContext(ctx), rawToken)
	if err != nil {
		return nil, fmt.Errorf("auth: oidc token verify: %w", err)
	}
	return claimsFromToken(idToken, cfg.groupClaim())
}

// parseGroups reads a group claim that may be a JSON string or array of strings.
func parseGroups(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var arr []string
	if err := json.Unmarshal(raw, &arr); err == nil {
		return arr
	}
	var one string
	if err := json.Unmarshal(raw, &one); err == nil && one != "" {
		return []string{one}
	}
	return nil
}

// authorized reports whether the claims satisfy the config (any-of) and returns
// the audit subject (email preferred, else sub).
func (cfg *OIDCConfig) authorized(cl *oidcClaims) (bool, string) {
	subject := cl.Email
	if subject == "" {
		subject = cl.Subject
	}
	if cfg.AnyAuthenticated {
		return true, subject
	}
	for _, allowed := range cfg.AllowedSubjects {
		if allowed != "" && (allowed == cl.Subject || allowed == cl.Email) {
			return true, subject
		}
	}
	for _, g := range cl.Groups {
		for _, allowed := range cfg.AllowedGroups {
			if allowed != "" && allowed == g {
				return true, subject
			}
		}
	}
	return false, ""
}

// OIDCVerifier is an auth.Verifier that validates a presented Bearer JWT against
// an OIDC issuer and authorizes by claims (design/14 §2.3). Browser redirect to
// the issuer is handled by the policy layer; this verifier covers API clients
// presenting a JWT they obtained out-of-band.
type OIDCVerifier struct {
	cfg   OIDCConfig
	cache *OIDCProviderCache
}

// NewOIDCVerifier builds an OIDC verifier using the shared provider cache.
func NewOIDCVerifier(cfg *OIDCConfig, cache *OIDCProviderCache) *OIDCVerifier {
	return &OIDCVerifier{cfg: *cfg, cache: cache}
}

// Verify validates a Bearer JWT and authorizes by claims. Discovery/verification
// failures yield (false, "") — never an error, so policy treats it as a miss.
func (v *OIDCVerifier) Verify(r *http.Request) (bool, string) {
	scheme, token, ok := parseAuthHeader(r)
	if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" {
		return false, ""
	}
	cl, err := v.cache.verifyToken(r.Context(), &v.cfg, token)
	if err != nil {
		return false, ""
	}
	return v.cfg.authorized(cl)
}

// Scheme returns "OIDC".
func (v *OIDCVerifier) Scheme() string { return "OIDC" }

// Challenge returns the API-client challenge; browsers are redirected by policy.
func (v *OIDCVerifier) Challenge() string { return "Bearer" }
