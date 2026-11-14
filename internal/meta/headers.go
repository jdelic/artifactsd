package meta

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// Response headers an artifact may set through content.headers are an
// ALLOWLIST (design/03 §6, artifactsd-m9e.5). Everything a writer stores there
// is emitted verbatim on the artifact's responses, on the same origin as the
// admin UI unless ARTIFACTSD_ADMIN_HOST is set, so origin-wide headers (NEL,
// Report-To, Clear-Site-Data, Service-Worker-Allowed, HSTS, …) and headers
// that corrupt the server's own protocol handling (ETag, Content-Range, …)
// must not be settable. Operators can extend the list
// (ARTIFACTSD_EXTRA_ALLOWED_HEADERS); entries then apply to all writers.

// allowedHeaders are permitted by name (canonical form).
var allowedHeaders = map[string]bool{
	"Cache-Control":                       true,
	"Content-Disposition":                 true,
	"Content-Language":                    true,
	"Content-Encoding":                    true,
	"Content-Security-Policy":             true,
	"Content-Security-Policy-Report-Only": true,
	"X-Robots-Tag":                        true,
	"Link":                                true,
	"Vary":                                true,
	"Expires":                             true,
	"Referrer-Policy":                     true,
	"Permissions-Policy":                  true,
	"Cross-Origin-Resource-Policy":        true,
	"Cross-Origin-Opener-Policy":          true,
	"Cross-Origin-Embedder-Policy":        true,
	"Timing-Allow-Origin":                 true,
	"X-Content-Type-Options":              true,
	"X-Frame-Options":                     true,
	// Content-Type is accepted on write but never served from content.headers:
	// the resolved content.content_type wins (FilterHeaders drops it).
	"Content-Type": true,
}

// deniedXHeaders are X-* headers that stay forbidden even though X-* is
// otherwise allowed: they steer proxies or the request pipeline.
var deniedXHeaders = map[string]bool{
	"X-Request-Id": true,
	"X-Sendfile":   true,
}

// deniedXPrefixes are X-* prefixes that stay forbidden.
var deniedXPrefixes = []string{"X-Forwarded-", "X-Accel-"}

// neverExtraHeaders may not be added by the operator either: hop-by-hop and
// framing headers, cookies, and Content-Type (resolved separately).
var neverExtraHeaders = map[string]bool{
	"Connection":          true,
	"Transfer-Encoding":   true,
	"Content-Length":      true,
	"Host":                true,
	"Keep-Alive":          true,
	"Upgrade":             true,
	"Te":                  true,
	"Trailer":             true,
	"Proxy-Authenticate":  true,
	"Proxy-Authorization": true,
	"Set-Cookie":          true,
	"Content-Type":        true,
}

// Limits on one document's content.headers.
const (
	maxHeaderNameLen  = 128
	maxHeaderValueLen = 8 * 1024
	maxHeadersPerDoc  = 32
)

// PolicyOptions are the operator settings a Policy is built from.
type PolicyOptions struct {
	// ExtraAllowedHeaders extends the content.headers allowlist.
	ExtraAllowedHeaders []string
}

// Policy carries operator-configured metadata rules into validation and
// serving. The meta package never reads configuration itself; callers build a
// Policy from config and pass it in. A nil *Policy applies the built-in
// defaults.
type Policy struct {
	extraHeaders map[string]bool
}

// NewPolicy builds a Policy, validating the options.
func NewPolicy(o PolicyOptions) (*Policy, error) {
	if err := ValidateExtraHeaders(o.ExtraAllowedHeaders); err != nil {
		return nil, err
	}
	p := &Policy{extraHeaders: map[string]bool{}}
	for _, h := range o.ExtraAllowedHeaders {
		p.extraHeaders[http.CanonicalHeaderKey(strings.TrimSpace(h))] = true
	}
	return p, nil
}

// ValidateExtraHeaders checks operator-supplied extra header names: each must
// be a legal header token and not one that may never be set from metadata.
func ValidateExtraHeaders(names []string) error {
	var bad []string
	for _, n := range names {
		n = strings.TrimSpace(n)
		switch {
		case !validHeaderName(n):
			bad = append(bad, fmt.Sprintf("%q is not a valid header name", n))
		case neverExtraHeaders[http.CanonicalHeaderKey(n)]:
			bad = append(bad, fmt.Sprintf("%q can never be set from metadata", http.CanonicalHeaderKey(n)))
		}
	}
	if len(bad) > 0 {
		return fmt.Errorf("extra allowed headers: %s", strings.Join(bad, "; "))
	}
	return nil
}

// HeaderAllowed reports whether content.headers may carry name.
func (p *Policy) HeaderAllowed(name string) bool {
	key := http.CanonicalHeaderKey(name)
	if allowedHeaders[key] || strings.HasPrefix(key, "Access-Control-") {
		return true
	}
	if p != nil && p.extraHeaders[key] {
		return true
	}
	if !strings.HasPrefix(key, "X-") || deniedXHeaders[key] {
		return false
	}
	for _, pre := range deniedXPrefixes {
		if strings.HasPrefix(key, pre) {
			return false
		}
	}
	return true
}

// headerProblems lists why a content.headers map is rejected at write time
// (empty = acceptable), sorted for stable messages.
func (p *Policy) headerProblems(headers map[string]string) []string {
	var probs []string
	if len(headers) > maxHeadersPerDoc {
		probs = append(probs, fmt.Sprintf("content.headers has %d entries (max %d)", len(headers), maxHeadersPerDoc))
	}
	for name, v := range headers {
		key := http.CanonicalHeaderKey(name)
		switch {
		case len(name) > maxHeaderNameLen:
			probs = append(probs, fmt.Sprintf("content.headers name %.32q… is longer than %d bytes", name, maxHeaderNameLen))
		case !validHeaderName(name):
			probs = append(probs, fmt.Sprintf("content.headers %q is not a valid header name", name))
		case !p.HeaderAllowed(name):
			probs = append(probs, fmt.Sprintf("content.headers %q is not in the allowed set, see docs", key))
		case !validHeaderValue(v):
			probs = append(probs, fmt.Sprintf("content.headers %q value must not contain CR, LF or NUL", key))
		case len(v) > maxHeaderValueLen:
			probs = append(probs, fmt.Sprintf("content.headers %q value is longer than %d bytes", key, maxHeaderValueLen))
		}
	}
	sort.Strings(probs)
	return probs
}

// canonicalHeaders returns a copy of headers with keys in HTTP-canonical form
// (http.CanonicalHeaderKey), so stored documents agree on casing with the
// resolved/served form (design/03 §6). Keys that canonicalize to the same name
// collapse to one entry; the value from the lexicographically-last raw key wins
// so the result is deterministic regardless of map iteration order. Returns the
// input unchanged when it is nil or empty.
func canonicalHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return headers
	}
	keys := make([]string, 0, len(headers))
	for k := range headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make(map[string]string, len(headers))
	for _, k := range keys {
		out[http.CanonicalHeaderKey(k)] = headers[k]
	}
	return out
}

// FilterHeaders returns a copy of headers safe to emit at serve time: only
// allowed names with well-formed values survive, and Content-Type is removed
// (the resolved content_type wins). It is applied as defense in depth even
// though writes are validated, and it is what keeps headers stored before the
// allowlist existed from being served. Keys are canonicalized. Returns nil if
// nothing remains.
func (p *Policy) FilterHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string]string, len(headers))
	for name, v := range headers {
		key := http.CanonicalHeaderKey(name)
		if key == "Content-Type" || !validHeaderName(name) || !p.HeaderAllowed(key) ||
			!validHeaderValue(v) || len(v) > maxHeaderValueLen {
			continue
		}
		out[key] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// validHeaderName reports whether s is a non-empty RFC 9110 token.
func validHeaderName(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0:
		default:
			return false
		}
	}
	return true
}

// validHeaderValue rejects values that could split or truncate a header.
func validHeaderValue(v string) bool {
	return !strings.ContainsAny(v, "\r\n\x00")
}
