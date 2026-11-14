package meta

import (
	"reflect"
	"strings"
	"testing"
)

func TestHeaderAllowed(t *testing.T) {
	var p *Policy // default policy
	allowed := []string{
		"Cache-Control", "cache-control", "Content-Disposition", "Content-Language", "Content-Encoding",
		"Content-Security-Policy", "Content-Security-Policy-Report-Only", "X-Robots-Tag", "Link", "Vary",
		"Expires", "Referrer-Policy", "Permissions-Policy", "Cross-Origin-Resource-Policy",
		"Cross-Origin-Opener-Policy", "Cross-Origin-Embedder-Policy", "Timing-Allow-Origin",
		"X-Content-Type-Options", "X-Frame-Options", "Access-Control-Allow-Origin",
		"access-control-expose-headers", "X-Custom-Thing", "Content-Type",
	}
	for _, h := range allowed {
		if !p.HeaderAllowed(h) {
			t.Errorf("HeaderAllowed(%q) = false, want true", h)
		}
	}
	denied := []string{
		"NEL", "Report-To", "Reporting-Endpoints", "Clear-Site-Data", "Service-Worker-Allowed",
		"Set-Cookie", "set-cookie", "Strict-Transport-Security", "Alt-Svc", "Origin-Agent-Cluster",
		"Refresh", "Location", "WWW-Authenticate", "ETag", "Last-Modified", "Accept-Ranges",
		"Content-Range", "Connection", "Transfer-Encoding", "Content-Length", "X-Request-Id",
		"x-request-id", "X-Forwarded-For", "X-Forwarded-Host", "X-Accel-Redirect", "X-Sendfile",
		"Server-Timing",
	}
	for _, h := range denied {
		if p.HeaderAllowed(h) {
			t.Errorf("HeaderAllowed(%q) = true, want false", h)
		}
	}
}

func TestNewPolicy_ExtraHeaders(t *testing.T) {
	p, err := NewPolicy(PolicyOptions{ExtraAllowedHeaders: []string{"server-timing"}})
	if err != nil {
		t.Fatal(err)
	}
	if !p.HeaderAllowed("Server-Timing") {
		t.Error("extra header not allowed")
	}
	for _, bad := range []string{"Set-Cookie", "Connection", "Content-Length", "Content-Type", "Bad Header", "", "Host"} {
		if _, err := NewPolicy(PolicyOptions{ExtraAllowedHeaders: []string{bad}}); err == nil {
			t.Errorf("NewPolicy(extra %q) = nil error, want error", bad)
		}
	}
}

func TestValidateHeaders(t *testing.T) {
	var p *Policy
	check := func(h map[string]string) error {
		d := &Document{Content: &Content{Headers: h}}
		return d.ValidateWith(p)
	}
	if err := check(map[string]string{"Cache-Control": "no-store"}); err != nil {
		t.Errorf("allowed header: %v", err)
	}
	err := check(map[string]string{"NEL": "{}", "Report-To": "{}"})
	if err == nil || !strings.Contains(err.Error(), `"Nel"`) || !strings.Contains(err.Error(), "not in the allowed set") {
		t.Errorf("NEL: err = %v", err)
	}
	if err := check(map[string]string{"X-Foo": "a\r\nSet-Cookie: x=1"}); err == nil {
		t.Error("CRLF in value accepted")
	}
	if err := check(map[string]string{"X-Foo": "a\x00b"}); err == nil {
		t.Error("NUL in value accepted")
	}
	if err := check(map[string]string{"X-Foo": strings.Repeat("a", 8*1024+1)}); err == nil {
		t.Error("over-long value accepted")
	}
	if err := check(map[string]string{"X-" + strings.Repeat("a", 127): "v"}); err == nil {
		t.Error("over-long name accepted")
	}
	many := map[string]string{}
	for i := range 33 {
		many["X-H"+strings.Repeat("a", i)] = "v"
	}
	if err := check(many); err == nil {
		t.Error("33 headers accepted")
	}
}

func TestFilterHeaders(t *testing.T) {
	var p *Policy
	got := p.FilterHeaders(map[string]string{
		"cache-control": "no-store",
		"Connection":    "close",
		"Content-Type":  "text/html",
		"NEL":           `{"report_to":"x"}`,
		"X-Thing":       "1",
	})
	want := map[string]string{"Cache-Control": "no-store", "X-Thing": "1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("FilterHeaders = %v, want %v", got, want)
	}
	if p.FilterHeaders(nil) != nil {
		t.Error("FilterHeaders(nil) should be nil")
	}
	if p.FilterHeaders(map[string]string{"Connection": "x"}) != nil {
		t.Error("FilterHeaders of only-disallowed should be nil")
	}
	// A bad value (CR/LF) is dropped at serve time too.
	if got := p.FilterHeaders(map[string]string{"X-A": "a\nb"}); got != nil {
		t.Errorf("CRLF value served: %v", got)
	}
}
