package main

import (
	"os"
	"path/filepath"
	"testing"
)

func envFrom(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestResolveConfig_Precedence(t *testing.T) {
	cfg := &fileConfig{
		Current: "default",
		Profiles: map[string]profile{
			"default": {BaseURL: "https://file.example", Token: "tok_file"},
			"staging": {BaseURL: "https://staging.example", Token: "tok_staging"},
		},
	}

	// File profile (current) wins when nothing else set.
	r, err := resolveConfig(cfg, "", "", "", envFrom(nil))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if r.baseURL != "https://file.example" || r.token != "tok_file" {
		t.Errorf("file resolution = %+v", r)
	}

	// Explicit profile selects it.
	r, _ = resolveConfig(cfg, "staging", "", "", envFrom(nil))
	if r.baseURL != "https://staging.example" {
		t.Errorf("profile resolution = %+v", r)
	}

	// Env overrides file.
	r, _ = resolveConfig(cfg, "", "", "", envFrom(map[string]string{envBaseURL: "https://env.example"}))
	if r.baseURL != "https://env.example" || r.token != "tok_file" {
		t.Errorf("env override = %+v", r)
	}

	// Flags override env and file.
	r, _ = resolveConfig(cfg, "", "https://flag.example", "tok_flag",
		envFrom(map[string]string{envBaseURL: "https://env.example"}))
	if r.baseURL != "https://flag.example" || r.token != "tok_flag" {
		t.Errorf("flag override = %+v", r)
	}
}

func TestResolveConfig_UnknownProfile(t *testing.T) {
	cfg := &fileConfig{Profiles: map[string]profile{"default": {}}}
	if _, err := resolveConfig(cfg, "nope", "", "", envFrom(nil)); err == nil {
		t.Error("expected error for unknown profile")
	}
	if _, err := resolveConfig(nil, "nope", "", "", envFrom(nil)); err == nil {
		t.Error("expected error for profile without config file")
	}
}

func TestLoadFileConfig_Missing(t *testing.T) {
	cfg, err := loadFileConfig(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err != nil {
		t.Fatalf("missing config should not error: %v", err)
	}
	if cfg != nil {
		t.Errorf("missing config = %+v, want nil", cfg)
	}
}

func TestLoadFileConfig_Parse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	body := "current: default\nprofiles:\n  default:\n    base_url: https://x.example\n    token: tok_x\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadFileConfig(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Current != "default" || cfg.Profiles["default"].BaseURL != "https://x.example" {
		t.Errorf("config = %+v", cfg)
	}
}

func TestDefaultConfigPath(t *testing.T) {
	if p := defaultConfigPath(envFrom(map[string]string{envConfig: "/custom/path.yaml"})); p != "/custom/path.yaml" {
		t.Errorf("explicit path = %q", p)
	}
	if p := defaultConfigPath(envFrom(map[string]string{"XDG_CONFIG_HOME": "/xdg"})); p != "/xdg/artifactctl/config.yaml" {
		t.Errorf("xdg path = %q", p)
	}
	if p := defaultConfigPath(envFrom(map[string]string{"HOME": "/home/u"})); p != "/home/u/.config/artifactctl/config.yaml" {
		t.Errorf("home path = %q", p)
	}
}
