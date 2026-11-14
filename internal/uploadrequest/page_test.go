package uploadrequest_test

import (
	"context"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestUploadPage_CSPWithNonce(t *testing.T) {
	ts := newDemo(t)
	id := createLink(t, ts, `{"collection":"demo","label":"Drop <b>here</b>"}`)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.BaseURL+"/r/"+id, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept", "text/html")
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("GET /r/: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}

	csp := resp.Header.Get("Content-Security-Policy")
	m := regexp.MustCompile(`script-src 'nonce-([A-Za-z0-9+/=_-]+)'`).FindStringSubmatch(csp)
	if m == nil {
		t.Fatalf("CSP lacks a script nonce: %q", csp)
	}
	nonce := m[1]
	for _, want := range []string{
		"default-src 'none'", "style-src 'nonce-" + nonce + "'", "connect-src 'self'",
		"form-action 'self'", "base-uri 'none'", "frame-ancestors 'none'",
	} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP %q missing %q", csp, want)
		}
	}
	page := string(body)
	if !strings.Contains(page, `<script nonce="`+nonce+`">`) || !strings.Contains(page, `<style nonce="`+nonce+`">`) {
		t.Error("script/style tags do not carry the response nonce")
	}
	if strings.Contains(page, "innerHTML") || strings.Contains(page, "onclick=") || strings.Contains(page, ` style="`) {
		t.Error("page uses innerHTML, an inline handler or an inline style attribute")
	}
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" || resp.Header.Get("Referrer-Policy") != "no-referrer" {
		t.Errorf("headers: nosniff=%q referrer=%q", resp.Header.Get("X-Content-Type-Options"), resp.Header.Get("Referrer-Policy"))
	}

	// A second response gets a fresh nonce.
	resp2, err := ts.Client().Do(req.Clone(context.Background()))
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.Header.Get("Content-Security-Policy") == csp {
		t.Error("nonce reused across responses")
	}
}

// TestUploadTemplate_NoInnerHTML is a cheap regression guard over the source.
func TestUploadTemplate_NoInnerHTML(t *testing.T) {
	src, err := os.ReadFile("templates/upload.html")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(src), "innerHTML") {
		t.Error("templates/upload.html must not use innerHTML")
	}
}
