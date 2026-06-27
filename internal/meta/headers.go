package meta

import (
	"net/http"
	"sort"
)

// unsafeHeaders are hop-by-hop or otherwise unsafe response headers that must
// never be set from metadata (design/03 §6, SPEC §7.2). Keyed in canonical form.
//
// Set-Cookie is rejected so an artifact cannot plant cookies on the shared
// admin/artifact origin (session fixation / pollution); author-supplied
// Content-Security-Policy is intentionally NOT rejected — it can only add
// restrictions on top of the sandbox CSP the server emits (artifactsd-c63).
var unsafeHeaders = map[string]bool{
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
}

// IsUnsafeHeader reports whether a header name is rejected from metadata.
func IsUnsafeHeader(name string) bool {
	return unsafeHeaders[http.CanonicalHeaderKey(name)]
}

// RejectedHeaders returns the canonical names of any unsafe headers present in
// the map, sorted. Used at write time to reject the document with a 400.
// Content-Type is not rejected (it is merely ignored in favor of the resolved
// content.content_type) and so is not listed here.
func RejectedHeaders(headers map[string]string) []string {
	var bad []string
	for name := range headers {
		if IsUnsafeHeader(name) {
			bad = append(bad, http.CanonicalHeaderKey(name))
		}
	}
	sort.Strings(bad)
	return bad
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

// FilterHeaders returns a copy of headers safe to emit at serve time: unsafe
// hop-by-hop headers are dropped and Content-Type is removed (the resolved
// content_type wins). Applied as defense-in-depth even though writes are
// validated. Keys are canonicalized. Returns nil if nothing remains.
func FilterHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string]string, len(headers))
	for name, v := range headers {
		key := http.CanonicalHeaderKey(name)
		if unsafeHeaders[key] || key == "Content-Type" {
			continue
		}
		out[key] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
