package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// Validate performs startup validation and fails fast, returning an error that
// lists every problem found (design/12 §7).
func (c *Config) Validate() error {
	var probs []string
	add := func(format string, args ...any) {
		probs = append(probs, fmt.Sprintf(format, args...))
	}

	// base_url must be an absolute http(s) URL.
	if c.Server.BaseURL == "" {
		add("base-url is required (ARTIFACTSD_BASE_URL / --base-url)")
	} else if u, err := url.Parse(c.Server.BaseURL); err != nil {
		add("base-url %q is not a valid URL: %v", c.Server.BaseURL, err)
	} else if !u.IsAbs() || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		add("base-url %q must be an absolute http(s) URL", c.Server.BaseURL)
	}

	if c.Server.Listen == "" {
		add("listen address must not be empty")
	}

	if !validLogLevel(c.Server.LogLevel) {
		add("log-level %q must be one of debug, info, warn, error", c.Server.LogLevel)
	}

	// Data dir must be creatable and writable.
	if c.Server.DataDir == "" {
		add("data dir must not be empty")
	} else if err := checkWritableDir(c.Server.DataDir); err != nil {
		add("data dir %q is not usable: %v", c.Server.DataDir, err)
	}

	c.validateTLS(add)
	c.validateAdmin(add)
	c.validatePolicy(add)
	c.validateLimits(add)

	if len(probs) == 0 {
		return nil
	}
	return fmt.Errorf("invalid configuration:\n  - %s", strings.Join(probs, "\n  - "))
}

func (c *Config) validateTLS(add func(string, ...any)) {
	switch c.TLS.Mode {
	case TLSOff:
		// nothing required
	case TLSFiles:
		if c.TLS.CertFile == "" || c.TLS.KeyFile == "" {
			add("tls=files requires both --tls-cert and --tls-key")
		}
	case TLSACME:
		if len(c.TLS.ACMEDomains) == 0 {
			add("tls=acme requires --acme-domains")
		}
	default:
		add("tls mode %q must be one of off, files, acme", c.TLS.Mode)
	}
}

func (c *Config) validateAdmin(add func(string, ...any)) {
	if c.Admin.Path == "" || !strings.HasPrefix(c.Admin.Path, "/") {
		add("admin path %q must start with '/'", c.Admin.Path)
	}

	oidcAny := c.OIDC.Enabled()
	if oidcAny {
		var missing []string
		if c.OIDC.Issuer == "" {
			missing = append(missing, "ARTIFACTSD_OIDC_ISSUER")
		}
		if c.OIDC.ClientID == "" {
			missing = append(missing, "ARTIFACTSD_OIDC_CLIENT_ID")
		}
		if c.OIDC.RedirectURL == "" {
			missing = append(missing, "ARTIFACTSD_OIDC_REDIRECT_URL")
		}
		if len(missing) > 0 {
			add("OIDC is partially configured; missing: %s", strings.Join(missing, ", "))
		}
	}

	basicAny := c.Admin.BasicUser != "" || c.Admin.BasicPassword != "" || c.Admin.BasicPasswordHash != ""
	if basicAny {
		if c.Admin.BasicUser == "" {
			add("admin basic fallback requires ARTIFACTSD_ADMIN_BASIC_USER")
		}
		if c.Admin.BasicPassword == "" && c.Admin.BasicPasswordHash == "" {
			add("admin basic fallback requires ARTIFACTSD_ADMIN_BASIC_PASSWORD or ..._HASH")
		}
	}
}

func (c *Config) validatePolicy(add func(string, ...any)) {
	switch c.Policy.Visibility {
	case VisibilityPublic, VisibilityPrivate:
	default:
		add("default visibility %q must be one of public, private", c.Policy.Visibility)
	}
	if c.Policy.DeleteAfterCreation < 0 {
		add("default delete-after-creation must not be negative")
	}
}

func (c *Config) validateLimits(add func(string, ...any)) {
	l := c.Limits
	if l.MaxUploadSize <= 0 {
		add("max upload size must be positive")
	}
	if l.MaxFilesPerCollection <= 0 {
		add("max files per collection must be positive")
	}
	if l.AuthFailMax <= 0 {
		add("auth-fail-max must be positive")
	}
	if l.AuthFailWindow <= 0 {
		add("auth-fail-window must be positive")
	}
	if l.JanitorInterval <= 0 {
		add("janitor interval must be positive")
	}
	if l.JanitorBatchSize <= 0 {
		add("janitor batch size must be positive")
	}
	if l.AccessUpdateThrottle < 0 {
		add("access-update-throttle must not be negative")
	}
	if l.UploadTTL <= 0 {
		add("upload TTL must be positive")
	}
}

func validLogLevel(level string) bool {
	switch strings.ToLower(level) {
	case "debug", "info", "warn", "error":
		return true
	default:
		return false
	}
}

// checkWritableDir ensures dir exists (creating it if needed) and is writable,
// by creating and removing a probe file.
func checkWritableDir(dir string) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("not a directory")
	}
	probe := filepath.Join(dir, ".artifactsd-write-probe")
	// G304: path is our own data dir plus a fixed probe filename, not user input.
	f, err := os.OpenFile(probe, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) //nolint:gosec
	if err != nil {
		return fmt.Errorf("not writable: %w", err)
	}
	_ = f.Close()
	_ = os.Remove(probe)
	return nil
}
