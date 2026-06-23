package meta

import (
	"net/http"
	"sort"
)

// unsafeHeaders are hop-by-hop or otherwise unsafe response headers that must
// never be set from metadata (design/03 §6, SPEC §7.2). Keyed in canonical form.
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
