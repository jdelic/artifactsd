package config

import (
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// Built-in defaults (design/12).
const (
	defaultListen                = ":8080"
	defaultDataDir               = "./artifactsd-data"
	defaultLogLevel              = "info"
	defaultAdminPath             = "/admin"
	defaultAdminGroupClaim       = "groups"
	defaultMaxUploadSize         = int64(1) << 30 // 1 GiB
	defaultMaxFilesPerCollection = 10000
	defaultAuthFailMax           = 10
	defaultJanitorBatchSize      = 500
)

var (
	defaultAuthFailWindow       = time.Minute
	defaultJanitorInterval      = 60 * time.Second
	defaultAccessUpdateThrottle = 60 * time.Second
	defaultUploadTTL            = time.Hour
)

// Getenv is the signature of os.Getenv; injectable for testing.
type Getenv func(string) string

// Load resolves configuration from args (flags) and the environment, applying
// flags > env > defaults precedence. The flag set parses args; env-only
// settings (OIDC, admin secrets, limits, default policy) are read from getenv.
//
// The returned Config is not yet validated; call Validate (or LoadAndValidate)
// before use.
func Load(args []string, getenv Getenv, out io.Writer) (*Config, error) {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}

	fs := flag.NewFlagSet("artifactsd", flag.ContinueOnError)
	fs.SetOutput(out)

	c := &Config{}

	// Server flags: default taken from env so flag > env > builtin holds.
	fs.StringVar(&c.Server.Listen, "listen", envStr(getenv, "ARTIFACTSD_LISTEN", defaultListen), "listen address")
	fs.StringVar(&c.Server.BaseURL, "base-url", envStr(getenv, "ARTIFACTSD_BASE_URL", ""), "public base URL (required)")
	fs.StringVar(&c.Server.DataDir, "data", envStr(getenv, "ARTIFACTSD_DATA_DIR", defaultDataDir), "data directory")
	fs.BoolVar(&c.Server.AutoMigrate, "auto-migrate", envBool(getenv, "ARTIFACTSD_AUTO_MIGRATE", true), "apply pending migrations on boot")
	fs.BoolVar(&c.Server.TrustProxy, "trust-proxy", envBool(getenv, "ARTIFACTSD_TRUST_PROXY", false), "trust X-Forwarded-* proxy headers")
	fs.StringVar(&c.Server.LogLevel, "log-level", envStr(getenv, "ARTIFACTSD_LOG_LEVEL", defaultLogLevel), "log level (debug|info|warn|error)")

	// TLS flags.
	var tlsMode string
	fs.StringVar(&tlsMode, "tls", envStr(getenv, "ARTIFACTSD_TLS", string(TLSOff)), "TLS mode (off|files|acme)")
	fs.StringVar(&c.TLS.CertFile, "tls-cert", envStr(getenv, "ARTIFACTSD_TLS_CERT_FILE", ""), "TLS certificate file (tls=files)")
	fs.StringVar(&c.TLS.KeyFile, "tls-key", envStr(getenv, "ARTIFACTSD_TLS_KEY_FILE", ""), "TLS key file (tls=files)")
	var acmeDomains string
	fs.StringVar(&acmeDomains, "acme-domains", envStr(getenv, "ARTIFACTSD_ACME_DOMAINS", ""), "comma-separated ACME domains (tls=acme)")
	fs.StringVar(&c.TLS.ACMEEmail, "acme-email", envStr(getenv, "ARTIFACTSD_ACME_EMAIL", ""), "ACME registration email (tls=acme)")
	fs.StringVar(&c.TLS.ACMECacheDir, "acme-cache", envStr(getenv, "ARTIFACTSD_ACME_CACHE_DIR", ""), "ACME cache dir (default <data>/acme)")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	c.TLS.Mode = TLSMode(tlsMode)
	c.TLS.ACMEDomains = splitList(acmeDomains)
	if c.TLS.ACMECacheDir == "" {
		c.TLS.ACMECacheDir = strings.TrimRight(c.Server.DataDir, "/") + "/acme"
	}

	// Admin & OIDC: env-only (secrets; design/12 §2).
	c.Admin = Admin{
		Path:              envStr(getenv, "ARTIFACTSD_ADMIN_PATH", defaultAdminPath),
		GroupClaim:        envStr(getenv, "ARTIFACTSD_ADMIN_GROUP_CLAIM", defaultAdminGroupClaim),
		Group:             getenv("ARTIFACTSD_ADMIN_GROUP"),
		BasicUser:         getenv("ARTIFACTSD_ADMIN_BASIC_USER"),
		BasicPassword:     getenv("ARTIFACTSD_ADMIN_BASIC_PASSWORD"),
		BasicPasswordHash: getenv("ARTIFACTSD_ADMIN_BASIC_PASSWORD_HASH"),
	}
	c.OIDC = OIDC{
		Issuer:       getenv("ARTIFACTSD_OIDC_ISSUER"),
		ClientID:     getenv("ARTIFACTSD_OIDC_CLIENT_ID"),
		ClientSecret: getenv("ARTIFACTSD_OIDC_CLIENT_SECRET"),
		RedirectURL:  getenv("ARTIFACTSD_OIDC_REDIRECT_URL"),
	}

	// Default object policy: env-only (design/12 §3).
	dap, err := loadDefaultPolicy(getenv)
	if err != nil {
		return nil, err
	}
	c.Policy = dap

	// Limits: env-only (design/12 §4).
	lim, err := loadLimits(getenv)
	if err != nil {
		return nil, err
	}
	c.Limits = lim

	// Bootstrap admin token: env-only (design/12 §2).
	c.Bootstrap = Bootstrap{
		Token: getenv("ARTIFACTSD_BOOTSTRAP_TOKEN"),
	}

	return c, nil
}

// LoadAndValidate loads configuration and runs startup validation, returning a
// ready-to-use Config or an error listing all problems.
func LoadAndValidate(args []string, getenv Getenv, out io.Writer) (*Config, error) {
	c, err := Load(args, getenv, out)
	if err != nil {
		return nil, err
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return c, nil
}

func loadDefaultPolicy(getenv Getenv) (DefaultPolicy, error) {
	p := DefaultPolicy{
		Visibility:       Visibility(envStr(getenv, "ARTIFACTSD_DEFAULT_VISIBILITY", string(VisibilityPublic))),
		DirectoryListing: envBool(getenv, "ARTIFACTSD_DEFAULT_DIRECTORY_LISTING", false),
		Crawlable:        envBool(getenv, "ARTIFACTSD_DEFAULT_CRAWLABLE", false),
		Indexable:        envBool(getenv, "ARTIFACTSD_DEFAULT_INDEXABLE", false),
		Sandbox:          envBool(getenv, "ARTIFACTSD_DEFAULT_SANDBOX", true),
	}
	d, err := envDuration(getenv, "ARTIFACTSD_DEFAULT_DELETE_AFTER_CREATION", 0)
	if err != nil {
		return p, err
	}
	p.DeleteAfterCreation = d
	return p, nil
}

func loadLimits(getenv Getenv) (Limits, error) {
	var errs []string
	pInt64 := func(key string, def int64) int64 {
		v, err := envInt64(getenv, key, def)
		if err != nil {
			errs = append(errs, err.Error())
		}
		return v
	}
	pInt := func(key string, def int) int {
		v, err := envInt(getenv, key, def)
		if err != nil {
			errs = append(errs, err.Error())
		}
		return v
	}
	pDur := func(key string, def time.Duration) time.Duration {
		v, err := envDuration(getenv, key, def)
		if err != nil {
			errs = append(errs, err.Error())
		}
		return v
	}

	lim := Limits{
		MaxUploadSize:         pInt64("ARTIFACTSD_MAX_UPLOAD_SIZE", defaultMaxUploadSize),
		MaxFilesPerCollection: pInt("ARTIFACTSD_MAX_FILES_PER_COLLECTION", defaultMaxFilesPerCollection),
		AuthFailMax:           pInt("ARTIFACTSD_AUTH_FAIL_MAX", defaultAuthFailMax),
		AuthFailWindow:        pDur("ARTIFACTSD_AUTH_FAIL_WINDOW", defaultAuthFailWindow),
		JanitorInterval:       pDur("ARTIFACTSD_JANITOR_INTERVAL", defaultJanitorInterval),
		JanitorBatchSize:      pInt("ARTIFACTSD_JANITOR_BATCH_SIZE", defaultJanitorBatchSize),
		AccessUpdateThrottle:  pDur("ARTIFACTSD_ACCESS_UPDATE_THROTTLE", defaultAccessUpdateThrottle),
		UploadTTL:             pDur("ARTIFACTSD_UPLOAD_TTL", defaultUploadTTL),
	}
	if len(errs) > 0 {
		return lim, fmt.Errorf("invalid limit configuration: %s", strings.Join(errs, "; "))
	}
	return lim, nil
}

// --- env helpers -----------------------------------------------------------

func envStr(getenv Getenv, key, def string) string {
	if v := getenv(key); v != "" {
		return v
	}
	return def
}

func envBool(getenv Getenv, key string, def bool) bool {
	v := getenv(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(strings.TrimSpace(v))
	if err != nil {
		return def
	}
	return b
}

func envInt(getenv Getenv, key string, def int) (int, error) {
	v := strings.TrimSpace(getenv(key))
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def, fmt.Errorf("%s: %q is not an integer", key, v)
	}
	return n, nil
}

func envInt64(getenv Getenv, key string, def int64) (int64, error) {
	v := strings.TrimSpace(getenv(key))
	if v == "" {
		return def, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return def, fmt.Errorf("%s: %q is not an integer", key, v)
	}
	return n, nil
}

func envDuration(getenv Getenv, key string, def time.Duration) (time.Duration, error) {
	v := strings.TrimSpace(getenv(key))
	if v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def, fmt.Errorf("%s: %q is not a duration", key, v)
	}
	return d, nil
}

func splitList(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
