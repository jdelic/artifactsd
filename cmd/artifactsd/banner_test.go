package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/jdelic/artifactsd/internal/config"
)

func TestPrintBanner_PlainToBuffer(t *testing.T) {
	cfg := &config.Config{}
	cfg.Server.Listen = ":8080"
	cfg.Server.BaseURL = "https://artifacts.example.com"
	cfg.Server.DataDir = "/data"
	cfg.TLS.Mode = config.TLSOff

	var buf bytes.Buffer
	printBanner(&buf, cfg)
	out := buf.String()

	// A non-*os.File writer is never a terminal, so no ANSI escapes leak.
	if strings.Contains(out, "\x1b[") {
		t.Errorf("banner to a buffer must not contain ANSI escapes:\n%q", out)
	}
	for _, want := range []string{"█████╗", ":8080", "https://artifacts.example.com", "self-contained artifact store"} {
		if !strings.Contains(out, want) {
			t.Errorf("banner missing %q in:\n%s", want, out)
		}
	}
}

func TestBannerUsesColor_NonFileIsFalse(t *testing.T) {
	if bannerUsesColor(&bytes.Buffer{}) {
		t.Error("a non-*os.File writer is not a terminal; color must be off")
	}
}

func TestBannerUsesColor_NoColorEnv(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	if bannerUsesColor(os.Stderr) {
		t.Error("NO_COLOR set must disable color regardless of the writer")
	}
}
