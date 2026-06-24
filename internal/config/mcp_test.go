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
	if m.Listen != defaultMCPListen {
		t.Errorf("Listen = %q, want %q", m.Listen, defaultMCPListen)
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
		"ARTIFACTSD_MCP_TOKEN":  "tok_abc",
	})
	m, err := LoadMCP(nil, env, io.Discard)
	if err != nil {
		t.Fatalf("LoadMCP: %v", err)
	}
	if m.Listen != ":9090" || m.BaseURL != "https://artifacts.example.com" || m.Token != "tok_abc" {
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
	m := &MCP{Listen: ":8081", BaseURL: "https://artifacts.example.com", Token: "tok_abc", LogLevel: "info"}
	if err := m.Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}
}

func TestMCPValidate_RequiresBaseURLAndToken(t *testing.T) {
	m := &MCP{Listen: ":8081", LogLevel: "info"}
	err := m.Validate()
	if err == nil {
		t.Fatal("Validate = nil, want an error")
	}
	for _, want := range []string{"base-url", "token"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err.Error(), want)
		}
	}
}

func TestMCPValidate_RejectsRelativeBaseURL(t *testing.T) {
	m := &MCP{Listen: ":8081", BaseURL: "artifacts.example.com", Token: "tok_abc", LogLevel: "info"}
	if err := m.Validate(); err == nil {
		t.Error("Validate = nil, want a relative base-url to be rejected")
	}
}

func TestMCPValidate_RejectsBadLogLevel(t *testing.T) {
	m := &MCP{Listen: ":8081", BaseURL: "https://artifacts.example.com", Token: "tok_abc", LogLevel: "verbose"}
	if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "log-level") {
		t.Errorf("Validate = %v, want a log-level error", err)
	}
}
