package config

import (
	"io"
	"strings"
	"testing"
	"time"
)

// envMap returns a Getenv backed by a map.
func envMap(m map[string]string) Getenv {
	return func(k string) string { return m[k] }
}

func TestDefaults(t *testing.T) {
	c, err := Load(nil, envMap(nil), io.Discard)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Server.Listen != defaultListen {
		t.Errorf("Listen = %q, want %q", c.Server.Listen, defaultListen)
	}
	if c.Server.DataDir != defaultDataDir {
		t.Errorf("DataDir = %q, want %q", c.Server.DataDir, defaultDataDir)
	}
	if !c.Server.AutoMigrate {
		t.Errorf("AutoMigrate = false, want true")
	}
	if c.Policy.Visibility != VisibilityPublic {
		t.Errorf("default Visibility = %q, want public", c.Policy.Visibility)
	}
	if !c.Policy.Sandbox {
		t.Errorf("default Sandbox = false, want true (artifact isolation on by default)")
	}
	if c.Limits.MaxUploadSize != 1<<30 {
		t.Errorf("MaxUploadSize = %d, want %d", c.Limits.MaxUploadSize, 1<<30)
	}
	if c.Limits.JanitorInterval != 60*time.Second {
		t.Errorf("JanitorInterval = %v, want 60s", c.Limits.JanitorInterval)
	}
	if c.TLS.Mode != TLSOff {
		t.Errorf("TLS.Mode = %q, want off", c.TLS.Mode)
	}
	if c.TLS.ACMECacheDir != defaultDataDir+"/acme" {
		t.Errorf("ACMECacheDir = %q, want %q", c.TLS.ACMECacheDir, defaultDataDir+"/acme")
	}
}

func TestDefaultSandboxToggle(t *testing.T) {
	c, err := Load(nil, envMap(map[string]string{"ARTIFACTSD_DEFAULT_SANDBOX": "false"}), io.Discard)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Policy.Sandbox {
		t.Error("ARTIFACTSD_DEFAULT_SANDBOX=false should disable the default sandbox")
	}
}

func TestPrecedence_FlagOverEnvOverDefault(t *testing.T) {
	env := envMap(map[string]string{
		"ARTIFACTSD_LISTEN":   ":9000",
		"ARTIFACTSD_DATA_DIR": "/env/data",
	})
	// Flag overrides env for listen; data dir falls through to env.
	c, err := Load([]string{"--listen", ":1234"}, env, io.Discard)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Server.Listen != ":1234" {
		t.Errorf("Listen = %q, want flag value :1234", c.Server.Listen)
	}
	if c.Server.DataDir != "/env/data" {
		t.Errorf("DataDir = %q, want env value /env/data", c.Server.DataDir)
	}
}

func TestEnvBoolAndDuration(t *testing.T) {
	env := envMap(map[string]string{
		"ARTIFACTSD_AUTO_MIGRATE":     "false",
		"ARTIFACTSD_JANITOR_INTERVAL": "30s",
		"ARTIFACTSD_UPLOAD_TTL":       "2h",
	})
	c, err := Load(nil, env, io.Discard)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Server.AutoMigrate {
		t.Errorf("AutoMigrate = true, want false from env")
	}
	if c.Limits.JanitorInterval != 30*time.Second {
		t.Errorf("JanitorInterval = %v, want 30s", c.Limits.JanitorInterval)
	}
	if c.Limits.UploadTTL != 2*time.Hour {
		t.Errorf("UploadTTL = %v, want 2h", c.Limits.UploadTTL)
	}
}

func TestLoad_InvalidDurationErrors(t *testing.T) {
	env := envMap(map[string]string{"ARTIFACTSD_UPLOAD_TTL": "nope"})
	if _, err := Load(nil, env, io.Discard); err == nil {
		t.Fatal("expected error for invalid duration, got nil")
	}
}

func TestACMEDomainsParsing(t *testing.T) {
	c, err := Load([]string{"--acme-domains", "a.example.com, b.example.com ,"}, envMap(nil), io.Discard)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []string{"a.example.com", "b.example.com"}
	if len(c.TLS.ACMEDomains) != len(want) {
		t.Fatalf("ACMEDomains = %v, want %v", c.TLS.ACMEDomains, want)
	}
	for i := range want {
		if c.TLS.ACMEDomains[i] != want[i] {
			t.Errorf("ACMEDomains[%d] = %q, want %q", i, c.TLS.ACMEDomains[i], want[i])
		}
	}
}

// validBase returns a Config that passes validation, using a temp data dir.
func validBase(t *testing.T) *Config {
	t.Helper()
	c, err := Load([]string{"--base-url", "https://x.example.com", "--data", t.TempDir()}, envMap(nil), io.Discard)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return c
}

func TestValidate_OK(t *testing.T) {
	if err := validBase(t).Validate(); err != nil {
		t.Fatalf("Validate: unexpected error: %v", err)
	}
}

func TestValidate_RequiresBaseURL(t *testing.T) {
	c := validBase(t)
	c.Server.BaseURL = ""
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "base-url") {
		t.Fatalf("expected base-url error, got %v", err)
	}
}

func TestValidate_RejectsRelativeBaseURL(t *testing.T) {
	c := validBase(t)
	c.Server.BaseURL = "/relative/path"
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("expected absolute-URL error, got %v", err)
	}
}

func TestValidate_TLSFilesRequiresCertKey(t *testing.T) {
	c := validBase(t)
	c.TLS.Mode = TLSFiles
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "tls=files") {
		t.Fatalf("expected tls=files error, got %v", err)
	}
}

func TestValidate_TLSACMERequiresDomains(t *testing.T) {
	c := validBase(t)
	c.TLS.Mode = TLSACME
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "acme") {
		t.Fatalf("expected tls=acme error, got %v", err)
	}
}

func TestValidate_PartialOIDC(t *testing.T) {
	c := validBase(t)
	c.OIDC.Issuer = "https://idp.example.com"
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "OIDC") {
		t.Fatalf("expected partial-OIDC error, got %v", err)
	}
}

func TestValidate_BasicFallbackNeedsUserAndSecret(t *testing.T) {
	c := validBase(t)
	c.Admin.BasicPassword = "hunter2"
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "ADMIN_BASIC_USER") {
		t.Fatalf("expected basic-user error, got %v", err)
	}
}

func TestValidate_BootstrapTokenAnyValueAllowed(t *testing.T) {
	c := validBase(t)
	c.Bootstrap.Token = "ci-secret-without-prefix"
	if err := c.Validate(); err != nil {
		t.Errorf("Validate with an unprefixed bootstrap token: %v", err)
	}
}

func TestValidate_InvalidVisibility(t *testing.T) {
	c := validBase(t)
	c.Policy.Visibility = "translucent"
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "visibility") {
		t.Fatalf("expected visibility error, got %v", err)
	}
}

func TestValidate_CollectsMultipleProblems(t *testing.T) {
	c := validBase(t)
	c.Server.BaseURL = ""
	c.Policy.Visibility = "bad"
	c.TLS.Mode = "weird"
	err := c.Validate()
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{"base-url", "visibility", "tls mode"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}
}

func TestAdminEnabled(t *testing.T) {
	c := validBase(t)
	if c.AdminEnabled() {
		t.Error("AdminEnabled = true with no admin config")
	}
	c.OIDC.Issuer = "https://idp"
	if !c.AdminEnabled() {
		t.Error("AdminEnabled = false with OIDC issuer set")
	}
}

func TestBootstrapToken(t *testing.T) {
	c, err := Load(nil, envMap(nil), io.Discard)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Bootstrap.Token != "" {
		t.Errorf("Bootstrap.Token = %q, want empty by default", c.Bootstrap.Token)
	}

	c, err = Load(nil, envMap(map[string]string{"ARTIFACTSD_BOOTSTRAP_TOKEN": "tok_ci-secret"}), io.Discard)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Bootstrap.Token != "tok_ci-secret" {
		t.Errorf("Bootstrap.Token = %q, want tok_ci-secret", c.Bootstrap.Token)
	}
}
