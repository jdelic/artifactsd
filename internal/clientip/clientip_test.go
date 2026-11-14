package clientip

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func mustResolver(t *testing.T, list string) *Resolver {
	t.Helper()
	var entries []string
	if list != "" {
		entries = strings.Split(list, ",")
	}
	r, err := New(entries)
	if err != nil {
		t.Fatalf("New(%q): %v", list, err)
	}
	return r
}

func req(remote string, xff ...string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/x", http.NoBody)
	r.RemoteAddr = remote
	for _, v := range xff {
		r.Header.Add("X-Forwarded-For", v)
	}
	return r
}

func TestClientIP(t *testing.T) {
	cases := []struct {
		name    string
		trusted string
		remote  string
		xff     []string
		want    string
	}{
		{"no proxies, header ignored", "", "198.51.100.1:5000", []string{"203.0.113.9"}, "198.51.100.1"},
		{"untrusted peer, header ignored", "10.0.0.0/8", "198.51.100.1:5000", []string{"203.0.113.9"}, "198.51.100.1"},
		{"trusted peer, client-supplied left entry skipped", "10.0.0.0/8", "10.0.0.1:5000", []string{"evil, 203.0.113.9"}, "203.0.113.9"},
		{"spoofed left entry ignored", "10.0.0.0/8", "10.0.0.1:5000", []string{"192.0.2.66, 203.0.113.9"}, "203.0.113.9"},
		{"chain of trusted proxies", "10.0.0.0/8", "10.0.0.3:5000", []string{"1.1.1.1, 198.51.100.7, 10.0.0.2"}, "198.51.100.7"},
		{"multiple header lines joined", "10.0.0.0/8", "10.0.0.3:5000", []string{"1.1.1.1", "198.51.100.7, 10.0.0.2"}, "198.51.100.7"},
		{"all entries trusted", "10.0.0.0/8", "10.0.0.3:5000", []string{"10.0.0.9, 10.0.0.2"}, "10.0.0.3"},
		{"garbage directly behind proxy returns peer", "10.0.0.0/8", "10.0.0.1:5000", []string{"<b>x</b>"}, "10.0.0.1"},
		{"garbage behind trusted hop returns last good", "10.0.0.0/8", "10.0.0.1:5000", []string{"<b>x</b>, 10.0.0.2"}, "10.0.0.2"},
		{"empty header", "10.0.0.0/8", "10.0.0.1:5000", []string{""}, "10.0.0.1"},
		{"ipv6 peer untrusted", "", "[2001:db8::1]:443", nil, "2001:db8::1"},
		{"ipv6 loopback peer", "", "[::1]:1234", nil, "::1"},
		{"ipv6 trusted proxy, ipv6 client", "::1", "[::1]:1234", []string{"2001:db8::42"}, "2001:db8::42"},
		{"bare ip entry is a /32", "10.0.0.1", "10.0.0.2:5000", []string{"203.0.113.9"}, "10.0.0.2"},
		{"xff entry with port", "10.0.0.0/8", "10.0.0.1:5000", []string{"203.0.113.9:4711"}, "203.0.113.9"},
		{"v4-mapped peer matches v4 prefix", "10.0.0.0/8", "[::ffff:10.0.0.1]:5000", []string{"203.0.113.9"}, "203.0.113.9"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := mustResolver(t, c.trusted)
			if got := res.ClientIP(req(c.remote, c.xff...)).String(); got != c.want {
				t.Errorf("ClientIP = %q, want %q", got, c.want)
			}
		})
	}
}

func TestClientIP_UnparseableRemoteAddr(t *testing.T) {
	res := mustResolver(t, "")
	if got := res.ClientIP(req("not-an-address")); got.IsValid() {
		t.Errorf("ClientIP = %v, want invalid addr", got)
	}
	if got := res.ClientIPString(req("not-an-address")); got != "" {
		t.Errorf("ClientIPString = %q, want empty", got)
	}
}

func TestForwardedHostAndProto(t *testing.T) {
	res := mustResolver(t, "10.0.0.0/8")

	direct := req("198.51.100.1:5000")
	direct.Header.Set("X-Forwarded-Host", "admin.example.com")
	direct.Header.Set("X-Forwarded-Proto", "https")
	if got := res.ForwardedHost(direct); got != "" {
		t.Errorf("ForwardedHost from untrusted peer = %q, want empty", got)
	}
	if got := res.ForwardedProto(direct); got != "" {
		t.Errorf("ForwardedProto from untrusted peer = %q, want empty", got)
	}

	proxied := req("10.0.0.1:5000")
	proxied.Header.Set("X-Forwarded-Host", "admin.example.com, other")
	proxied.Header.Set("X-Forwarded-Proto", " HTTPS ,http")
	if got := res.ForwardedHost(proxied); got != "admin.example.com" {
		t.Errorf("ForwardedHost = %q, want admin.example.com", got)
	}
	if got := res.ForwardedProto(proxied); got != "HTTPS" {
		t.Errorf("ForwardedProto = %q, want HTTPS", got)
	}
}

func TestNew(t *testing.T) {
	r, err := New([]string{" 10.0.0.0/8 ", "192.0.2.1", "::1", "2001:db8::/32"})
	if err != nil {
		t.Fatal(err)
	}
	if got := r.String(); got != "10.0.0.0/8,192.0.2.1/32,::1/128,2001:db8::/32" {
		t.Errorf("String = %q", got)
	}
	for _, bad := range []string{"10.0.0.0/33", "example.com", "10.0.0.1/8x", ""} {
		if _, err := New([]string{bad}); err == nil {
			t.Errorf("New(%q) = nil error, want error", bad)
		}
	}
	empty, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	if empty.Enabled() || empty.String() != "" {
		t.Errorf("empty resolver: Enabled=%v String=%q", empty.Enabled(), empty.String())
	}
	// Non-canonical prefixes are masked so "10.1.2.3/8" means 10.0.0.0/8.
	masked, err := New([]string{"10.1.2.3/8"})
	if err != nil {
		t.Fatal(err)
	}
	if masked.String() != "10.0.0.0/8" {
		t.Errorf("masked = %q", masked.String())
	}
}

func TestNilResolverIgnoresHeaders(t *testing.T) {
	var res *Resolver
	r := req("10.0.0.1:5000", "203.0.113.9")
	r.Header.Set("X-Forwarded-Host", "h")
	if got := res.ClientIPString(r); got != "10.0.0.1" {
		t.Errorf("nil resolver ClientIP = %q", got)
	}
	if res.ForwardedHost(r) != "" || res.ForwardedProto(r) != "" {
		t.Error("nil resolver must ignore forwarded headers")
	}
}
