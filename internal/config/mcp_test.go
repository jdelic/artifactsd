package config

import (
	"io"
	"strings"
	"testing"
)

func TestLoadMCP_Defaults(t *testing.T) {
	m, err := LoadMCP(nil, envMap(nil), io.Discard)
	if err != nil {
		t.Fatalf("LoadMCP: %v", err)
	}
	if m.Listen != "127.0.0.1:8081" || !m.ListenIsLoopback() {
		t.Errorf("Listen = %q, want loopback 127.0.0.1:8081", m.Listen)
	}
	if m.BaseURL != "" || m.Token != "" {
		t.Errorf("BaseURL/Token = %q/%q, want both empty by default", m.BaseURL, m.Token)
	}
	if m.LogLevel != defaultLogLevel {
		t.Errorf("LogLevel = %q, want %q", m.LogLevel, defaultLogLevel)
	}
}

func TestLoadMCP_Env(t *testing.T) {
	env := envMap(map[string]string{
		"ARTIFACTSD_MCP_LISTEN": ":9090",
		"ARTIFACTSD_BASE_URL":   "https://artifacts.example.com",
	})
	m, err := LoadMCP(nil, env, io.Discard)
	if err != nil {
		t.Fatalf("LoadMCP: %v", err)
	}
	if m.Listen != ":9090" || m.BaseURL != "https://artifacts.example.com" || m.ListenIsLoopback() {
		t.Errorf("m = %+v", m)
	}
}

func TestLoadMCP_FlagOverEnv(t *testing.T) {
	env := envMap(map[string]string{"ARTIFACTSD_MCP_LISTEN": ":9090"})
	m, err := LoadMCP([]string{"-listen", ":7070"}, env, io.Discard)
	if err != nil {
		t.Fatalf("LoadMCP: %v", err)
	}
	if m.Listen != ":7070" {
		t.Errorf("Listen = %q, want flag to win over env", m.Listen)
	}
}

func TestMCPValidate_OK(t *testing.T) {
	m := &MCP{Listen: "127.0.0.1:8081", BaseURL: "https://artifacts.example.com", LogLevel: "info"}
	if err := m.Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}
}

func TestMCPValidate_RequiresBaseURL(t *testing.T) {
	m := &MCP{Listen: ":8081", LogLevel: "info"}
	if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "base-url") {
		t.Fatalf("Validate = %v, want a base-url error", err)
	}
}

// TestMCPValidate_RejectsRemovedToken: the adapter no longer holds a token;
// setting the old one must stop startup, not be silently ignored
// (artifactsd-m9e.18).
func TestMCPValidate_RejectsRemovedToken(t *testing.T) {
	_, err := LoadAndValidateMCP(nil, envMap(map[string]string{
		"ARTIFACTSD_BASE_URL":  "https://artifacts.example.com",
		"ARTIFACTSD_MCP_TOKEN": "tok_abc",
	}), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "no longer supported") || !strings.Contains(err.Error(), "Authorization: Bearer") {
		t.Errorf("err = %v, want a configuration error explaining the new model", err)
	}
	if _, err := LoadAndValidateMCP([]string{"-token", "tok_abc", "-base-url", "https://a.example.com"}, envMap(nil), io.Discard); err == nil {
		t.Error("--token accepted")
	}
}

func TestMCPUsage_ExplainsTokens(t *testing.T) {
	var buf strings.Builder
	_, _ = LoadMCP([]string{"-h"}, envMap(nil), &buf)
	out := buf.String()
	head := out[:min(len(out), 600)]
	for _, want := range []string{"stores NO credentials", "Authorization: Bearer", "ARTIFACTSD_TOKEN"} {
		if !strings.Contains(head, want) {
			t.Errorf("help does not lead with %q:\n%s", want, head)
		}
	}
}

func TestMCPValidate_RejectsRelativeBaseURL(t *testing.T) {
	m := &MCP{Listen: ":8081", BaseURL: "artifacts.example.com", LogLevel: "info"}
	if err := m.Validate(); err == nil {
		t.Error("Validate = nil, want a relative base-url to be rejected")
	}
}

func TestMCPValidate_RejectsBadLogLevel(t *testing.T) {
	m := &MCP{Listen: ":8081", BaseURL: "https://artifacts.example.com", LogLevel: "verbose"}
	if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "log-level") {
		t.Errorf("Validate = %v, want a log-level error", err)
	}
}
