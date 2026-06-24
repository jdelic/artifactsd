package main

import (
	"bytes"
	"testing"
)

// TestNewUploadProgress_DisabledForNonTerminal covers the common case (CI,
// scripted use, redirected output): a bytes.Buffer is never a terminal, so
// the progress bar must be a no-op, leaving stdout/stderr untouched.
func TestNewUploadProgress_DisabledForNonTerminal(t *testing.T) {
	var buf bytes.Buffer
	p := newUploadProgress(&buf, 3, false, false)
	if p.enabled {
		t.Fatal("progress enabled for a non-terminal writer")
	}
	p.advance("a.txt")
	p.advance("b.txt")
	p.finish()
	if buf.Len() != 0 {
		t.Errorf("buf = %q, want no output when disabled", buf.String())
	}
}

func TestNewUploadProgress_DisabledForQuietOrJSON(t *testing.T) {
	var buf bytes.Buffer
	if p := newUploadProgress(&buf, 3, true, false); p.enabled {
		t.Error("progress enabled with --quiet")
	}
	if p := newUploadProgress(&buf, 3, false, true); p.enabled {
		t.Error("progress enabled with --output json")
	}
}

func TestNewUploadProgress_DisabledForZeroFiles(t *testing.T) {
	var buf bytes.Buffer
	if p := newUploadProgress(&buf, 0, false, false); p.enabled {
		t.Error("progress enabled with zero files")
	}
}
