package main

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Client-side configuration & auth resolution (design/10 §1). This file is a
// convenience for the human-facing CLI and is entirely separate from the server
// config, which is flags+env only (design/12).

// envBaseURL and envToken are the environment overrides (design/10 §1).
const (
	envBaseURL = "ARTIFACTSD_BASE_URL"
	envToken   = "ARTIFACTSD_TOKEN"
	envConfig  = "ARTIFACTCTL_CONFIG"
)

// fileConfig is the on-disk ~/.config/artifactctl/config.yaml shape.
type fileConfig struct {
	Current  string             `yaml:"current"`
	Profiles map[string]profile `yaml:"profiles"`
}

type profile struct {
	BaseURL string `yaml:"base_url"`
	Token   string `yaml:"token"`
}

// defaultConfigPath returns $ARTIFACTCTL_CONFIG or the XDG default path.
func defaultConfigPath(env func(string) string) string {
	if p := env(envConfig); p != "" {
		return p
	}
	if xdg := env("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "artifactctl", "config.yaml")
	}
	return filepath.Join(env("HOME"), ".config", "artifactctl", "config.yaml")
}

// loadFileConfig reads and parses the config file. A missing file is not an
// error (returns nil, nil) — the CLI works fully from flags/env.
func loadFileConfig(path string) (*fileConfig, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is the CLI's own config file (flag/env/XDG default)
	if os.IsNotExist(err) {
		return nil, nil //nolint:nilnil // "no config file" is a valid, non-error state
	}
	if err != nil {
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}
	var cfg fileConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config %s: %w", path, err)
	}
	return &cfg, nil
}

// resolved is the effective connection config after applying precedence.
type resolved struct {
	baseURL string
	token   string
}

// resolveConfig applies the precedence order file < env < flags (design/10 §1).
// profileName selects a profile; when empty the file's `current` is used. An
// explicitly requested profile that does not exist is an error.
func resolveConfig(cfg *fileConfig, profileName, flagBaseURL, flagToken string, env func(string) string) (resolved, error) {
	var r resolved

	if cfg != nil {
		name := profileName
		if name == "" {
			name = cfg.Current
		}
		if name != "" {
			p, ok := cfg.Profiles[name]
			if !ok {
				if profileName != "" {
					return r, fmt.Errorf("profile %q not found in config", profileName)
				}
			} else {
				r.baseURL = p.BaseURL
				r.token = p.Token
			}
		}
	} else if profileName != "" {
		return r, fmt.Errorf("profile %q requested but no config file found", profileName)
	}

	if v := env(envBaseURL); v != "" {
		r.baseURL = v
	}
	if v := env(envToken); v != "" {
		r.token = v
	}
	if flagBaseURL != "" {
		r.baseURL = flagBaseURL
	}
	if flagToken != "" {
		r.token = flagToken
	}
	return r, nil
}
