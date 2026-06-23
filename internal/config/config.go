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
	Server Server
	TLS    TLS
	Admin  Admin
	OIDC   OIDC
	Policy DefaultPolicy
	Limits Limits
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
	Path              string
	GroupClaim        string
	Group             string
	BasicUser         string
	BasicPassword     string // cleartext; hashed at startup, never persisted
	BasicPasswordHash string // argon2id hash (preferred over cleartext)
}

// OIDC holds OpenID Connect settings for admin login.
type OIDC struct {
	Issuer       string
	ClientID     string
	ClientSecret string
	RedirectURL  string
}

// Enabled reports whether enough OIDC configuration is present to attempt
// admin login via OIDC.
func (o OIDC) Enabled() bool {
	return o.Issuer != "" || o.ClientID != "" || o.ClientSecret != "" || o.RedirectURL != ""
}

// DefaultPolicy is the server-defaults layer of metadata resolution
// (design/03 §2, design/12 §3).
type DefaultPolicy struct {
	Visibility          Visibility
	DirectoryListing    bool
	Crawlable           bool
	Indexable           bool
	DeleteAfterCreation time.Duration // zero = unset
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
