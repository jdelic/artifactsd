package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
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

// oidcNegativeTTL is how long a failed discovery is remembered: requests in
// that window get the cached error without any network I/O.
const oidcNegativeTTL = 60 * time.Second

// oidcHTTPTimeout bounds discovery and JWKS requests.
const oidcHTTPTimeout = 10 * time.Second

// OIDCProviderCache memoizes go-oidc providers (issuer discovery + JWKS) so each
// issuer is discovered once. It is safe for concurrent use.
//
// Discovery is server-side network I/O to a URL taken from object metadata, so
// it only ever happens for issuers on the operator's allowlist
// (WithAllowedIssuers; artifactsd-m9e.21). A cache with no allowlist allows
// nothing. Failed discoveries are negatively cached for oidcNegativeTTL, and
// concurrent first requests for one issuer share a single discovery.
type OIDCProviderCache struct {
	httpClient *http.Client
	log        *slog.Logger
	now        func() time.Time

	mu        sync.Mutex
	allowed   map[string]bool // normalized issuer → allowed
	providers map[string]*oidc.Provider
	failures  map[string]failedDiscovery
	inflight  map[string]*discovery
	warned    map[string]bool
}

type failedDiscovery struct {
	err   error
	until time.Time
}

type discovery struct {
	done chan struct{}
	p    *oidc.Provider
	err  error
}

// NewOIDCProviderCache returns an empty cache. Pass a non-nil client to override
// the HTTP transport used for discovery/JWKS (tests); either way the client
// gets a timeout and refuses redirects to another host.
func NewOIDCProviderCache(client *http.Client) *OIDCProviderCache {
	var c http.Client
	if client != nil {
		c = *client
	}
	if c.Timeout == 0 {
		c.Timeout = oidcHTTPTimeout
	}
	c.CheckRedirect = sameHostRedirectsOnly
	return &OIDCProviderCache{
		httpClient: &c,
		now:        time.Now,
		allowed:    map[string]bool{},
		providers:  map[string]*oidc.Provider{},
		failures:   map[string]failedDiscovery{},
		inflight:   map[string]*discovery{},
		warned:     map[string]bool{},
	}
}

// sameHostRedirectsOnly refuses redirects to a different host[:port], so an
// allowlisted issuer cannot bounce discovery or JWKS fetches elsewhere.
func sameHostRedirectsOnly(req *http.Request, via []*http.Request) error {
	if len(via) >= 5 {
		return errors.New("auth: oidc: too many redirects")
	}
	if !strings.EqualFold(req.URL.Host, via[0].URL.Host) {
		return fmt.Errorf("auth: oidc: refusing redirect from %s to another host", via[0].URL.Host)
	}
	return nil
}

// WithAllowedIssuers sets the issuer allowlist (exact match, ignoring one
// trailing slash).
func (c *OIDCProviderCache) WithAllowedIssuers(issuers []string) *OIDCProviderCache {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.allowed = map[string]bool{}
	for _, iss := range issuers {
		if n := NormalizeIssuer(iss); n != "" {
			c.allowed[n] = true
		}
	}
	return c
}

// WithLogger sets the logger used to warn (once per issuer) about objects that
// name a non-allowlisted issuer.
func (c *OIDCProviderCache) WithLogger(l *slog.Logger) *OIDCProviderCache {
	c.log = l
	return c
}

// NormalizeIssuer trims surrounding space and one trailing slash, the form in
// which issuers are compared against the allowlist.
func NormalizeIssuer(issuer string) string {
	return strings.TrimSuffix(strings.TrimSpace(issuer), "/")
}

// IssuerAllowed reports whether issuer is on the allowlist. A refused issuer
// is logged at WARN once.
func (c *OIDCProviderCache) IssuerAllowed(issuer string) bool {
	n := NormalizeIssuer(issuer)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.allowed[n] {
		return true
	}
	if c.log != nil && !c.warned[n] {
		c.warned[n] = true
		c.log.Warn("object metadata names an OIDC issuer that is not allowed; its oidc auth entries are ignored (set ARTIFACTSD_OIDC_ALLOWED_ISSUERS)",
			"issuer", n)
	}
	return false
}

// clientContext injects the cache's HTTP client for go-oidc.
func (c *OIDCProviderCache) clientContext(ctx context.Context) context.Context {
	return oidc.ClientContext(ctx, c.httpClient)
}

// provider returns the cached provider for issuer, discovering it on first use.
// Only allowlisted issuers are ever discovered.
func (c *OIDCProviderCache) provider(ctx context.Context, issuer string) (*oidc.Provider, error) {
	if !c.IssuerAllowed(issuer) {
		return nil, fmt.Errorf("auth: oidc issuer %q is not allowed", issuer)
	}
	key := NormalizeIssuer(issuer)

	c.mu.Lock()
	if p, ok := c.providers[key]; ok {
		c.mu.Unlock()
		return p, nil
	}
	if f, ok := c.failures[key]; ok && c.now().Before(f.until) {
		c.mu.Unlock()
		return nil, f.err
	}
	if d, ok := c.inflight[key]; ok {
		c.mu.Unlock()
		select {
		case <-d.done:
			return d.p, d.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	d := &discovery{done: make(chan struct{})}
	c.inflight[key] = d
	c.mu.Unlock()

	// Discovery outlives a single caller's cancellation so waiters still get a
	// result; it is bounded by the HTTP client timeout.
	dctx, cancel := context.WithTimeout(c.clientContext(context.WithoutCancel(ctx)), oidcHTTPTimeout)
	defer cancel()
	p, err := oidc.NewProvider(dctx, issuer)
	if err != nil {
		err = fmt.Errorf("auth: oidc discovery for %q: %w", issuer, err)
	}

	c.mu.Lock()
	delete(c.inflight, key)
	if err != nil {
		c.failures[key] = failedDiscovery{err: err, until: c.now().Add(oidcNegativeTTL)}
	} else {
		delete(c.failures, key)
		c.providers[key] = p
	}
	c.mu.Unlock()
	d.p, d.err = p, err
	close(d.done)
	return p, err
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

// AuthorizesIdentity evaluates the entry's any-of rules against an identity
// that was already authenticated by an OIDC login (a viewer session,
// artifactsd-m9e.22), returning the audit subject. groupClaim names the claim
// the login read groups from: group rules only apply when it is the claim this
// entry expects, otherwise only subject/email and any_authenticated rules can
// match.
func (cfg *OIDCConfig) AuthorizesIdentity(subject, email string, groups []string, groupClaim string) (bool, string) {
	cl := &oidcClaims{Subject: subject, Email: email}
	if groupClaim == cfg.groupClaim() {
		cl.Groups = groups
	}
	return cfg.authorized(cl)
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
