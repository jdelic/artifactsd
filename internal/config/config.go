// Package config loads and validates server configuration from command-line
// flags and ARTIFACTSD_* environment variables.
//
// Per operator decision F4/G (design/12), artifactsd has no config file:
// configuration is flags + environment only, following the 12factor
// methodology. Precedence is flags > env > built-in defaults.
package config

import "time"

// TLSMode selects how the server terminates TLS.
type TLSMode string

const (
	// TLSOff serves plain HTTP (e.g. behind a reverse proxy).
	TLSOff TLSMode = "off"
	// TLSFiles serves HTTPS from a static cert/key pair.
	TLSFiles TLSMode = "files"
	// TLSACME serves HTTPS with automatic Let's Encrypt certificates.
	TLSACME TLSMode = "acme"
)

// Visibility is the default object visibility (the base layer of metadata
// resolution; design/03 §2).
type Visibility string

const (
	// VisibilityPublic serves objects to anyone.
	VisibilityPublic Visibility = "public"
	// VisibilityPrivate requires authentication/authorization to serve.
	VisibilityPrivate Visibility = "private"
)

// Config is the fully-resolved server configuration. It is immutable after
// Load+Validate succeed.
type Config struct {
	Server    Server
	TLS       TLS
	Admin     Admin
	OIDC      OIDC
	Policy    DefaultPolicy
	Limits    Limits
	Bootstrap Bootstrap
}

// Server holds core HTTP/server settings.
type Server struct {
	Listen      string
	BaseURL     string
	DataDir     string
	AutoMigrate bool
	TrustProxy  bool
	LogLevel    string
}

// TLS holds TLS termination settings (decision D4).
type TLS struct {
	Mode         TLSMode
	CertFile     string
	KeyFile      string
	ACMEDomains  []string
	ACMEEmail    string
	ACMECacheDir string
}

// Admin holds admin-UI gating settings. OIDC is the primary mechanism; the
// basic credentials are a fallback (design/14).
type Admin struct {
	Path       string
	GroupClaim string
	Group      string
	// Subjects is the portable subject/email allowlist (ARTIFACTSD_ADMIN_SUBJECTS):
	// an authenticated OIDC identity is an admin if its sub OR email matches any
	// entry. sub is always present and email is a standard claim, so this works
	// on every IdP — the universal fallback when group claims are unavailable
	// (e.g. Google Workspace). It is an any-of predicate alongside Group.
	Subjects          []string
	BasicUser         string
	BasicPassword     string // cleartext; hashed at startup, never persisted
	BasicPasswordHash string // argon2id hash (preferred over cleartext)
}

// OIDCCallbackPath is the route the OIDC provider redirects back to after admin
// login (Authorization Code + PKCE). The redirect URL defaults to the public
// base URL joined with this path, and this exact URL is what must be registered
// as an allowed redirect URI at the OIDC provider.
const OIDCCallbackPath = "/auth/oidc/callback"

// OIDC holds OpenID Connect settings for admin login.
type OIDC struct {
	Issuer       string
	ClientID     string
	ClientSecret string
	// RedirectURL is the full absolute URL the provider redirects back to after
	// login. It is sent verbatim as the OAuth2 redirect_uri in both the
	// authorization request and the token exchange, so it must (a) resolve to
	// artifactsd's OIDCCallbackPath route and (b) be whitelisted at the provider.
	// When left unset it defaults to <base_url>+OIDCCallbackPath (see Load);
	// override it only when artifactsd's callback is reached at a different
	// external URL than base_url (e.g. behind a proxy or a separate admin origin).
	RedirectURL string
	// Scopes are the OAuth2 scopes requested at login. It defaults to the
	// standard "openid email profile" (ARTIFACTSD_OIDC_SCOPES overrides). "groups"
	// is deliberately NOT requested by default: it is not a standard OIDC scope,
	// many IdPs reject it (invalid_scope), and dropping it does not remove groups
	// when the IdP emits them via its own mapper. Operators whose IdP needs a
	// provider-specific scope to emit the group claim can add it back here. The
	// openid scope is always included regardless of this setting.
	Scopes []string
}

// Enabled reports whether enough OIDC configuration is present to attempt
// admin login via OIDC.
func (o *OIDC) Enabled() bool {
	return o.Issuer != "" || o.ClientID != "" || o.ClientSecret != "" || o.RedirectURL != ""
}

// Bootstrap holds the one-time admin token seeded at startup so a fresh
// deployment has a way into the management API without direct DB access
// (design/12 §2; there is otherwise no path to mint the first API token).
type Bootstrap struct {
	// Token is a cleartext secret chosen by the operator. If set, runServe
	// seeds it as an admin-scoped API token on every boot, but only inserts a
	// row the first time (idempotent on its hash) — restarting with the same
	// value is a no-op, not a duplicate token.
	Token string
}

// DefaultPolicy is the server-defaults layer of metadata resolution
// (design/03 §2, design/12 §3).
type DefaultPolicy struct {
	Visibility          Visibility
	DirectoryListing    bool
	Crawlable           bool
	Indexable           bool
	DeleteAfterCreation time.Duration // zero = unset
	// Sandbox is the default for whether active artifact responses are served
	// with a CSP sandbox (an opaque origin) so artifact JavaScript cannot drive
	// or read the same-origin admin UI (design/14 §7, artifactsd-c63). Default
	// true; a per-object content.sandbox overrides it.
	Sandbox bool
}

// Limits holds size/rate/janitor/upload knobs (design/12 §4).
type Limits struct {
	MaxUploadSize         int64
	MaxFilesPerCollection int
	AuthFailMax           int
	AuthFailWindow        time.Duration
	JanitorInterval       time.Duration
	JanitorBatchSize      int
	AccessUpdateThrottle  time.Duration
	UploadTTL             time.Duration
}

// AdminEnabled reports whether any admin access mechanism is configured.
func (c *Config) AdminEnabled() bool {
	return c.OIDC.Enabled() || c.Admin.BasicUser != ""
}
