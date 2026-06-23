package server

import (
	"bytes"
	"context"
	"testing"
)

func TestACMEManager_HostPolicy(t *testing.T) {
	var buf bytes.Buffer
	s := serverWithEnv(t, map[string]string{
		"ARTIFACTSD_TLS":            "acme",
		"ARTIFACTSD_ACME_DOMAINS":   "a.example.com,b.example.com",
		"ARTIFACTSD_ACME_EMAIL":     "ops@example.com",
		"ARTIFACTSD_ACME_CACHE_DIR": t.TempDir(),
	}, &buf)

	m := s.acmeManager()
	if m.Email != "ops@example.com" {
		t.Errorf("email = %q", m.Email)
	}
	ctx := context.Background()
	if err := m.HostPolicy(ctx, "a.example.com"); err != nil {
		t.Errorf("whitelisted host rejected: %v", err)
	}
	if err := m.HostPolicy(ctx, "evil.example.com"); err == nil {
		t.Error("non-whitelisted host accepted")
	}
}
