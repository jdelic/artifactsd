package auth

import (
	"net/http"
	"strings"
)

// Verifier checks whether an HTTP request satisfies one configured credential
// (design/06 §1.4). An object's effective access.auth list compiles to a slice
// of Verifiers; access is granted if ANY of them verifies (any-of).
type Verifier interface {
	// Verify reports whether the request satisfies this credential and, if so,
	// the authenticated subject (token prefix for bearer, username for basic,
	// sub/email for oidc) for audit logging.
	Verify(r *http.Request) (ok bool, subject string)
	// Scheme is the auth scheme name ("Bearer", "Basic", "OIDC").
	Scheme() string
	// Challenge is this verifier's WWW-Authenticate fragment (e.g.
	// `Basic realm="artifactsd"`).
	Challenge() string
}

// Authenticate evaluates verifiers any-of and returns the first match. With no
// verifiers it returns ok=false; callers treat an empty list as "public" at the
// policy layer, not here.
func Authenticate(verifiers []Verifier, r *http.Request) (ok bool, subject, scheme string) {
	for _, v := range verifiers {
		if matched, subj := v.Verify(r); matched {
			return true, subj, v.Scheme()
		}
	}
	return false, "", ""
}

// ComposeChallenge builds a WWW-Authenticate header value from the distinct
// challenges of the verifiers, preserving order.
func ComposeChallenge(verifiers []Verifier) string {
	seen := map[string]bool{}
	parts := make([]string, 0, len(verifiers))
	for _, v := range verifiers {
		c := v.Challenge()
		if c == "" || seen[c] {
			continue
		}
		seen[c] = true
		parts = append(parts, c)
	}
	return strings.Join(parts, ", ")
}

// parseAuthHeader splits an Authorization header into scheme and credentials.
func parseAuthHeader(r *http.Request) (scheme, rest string, ok bool) {
	h := r.Header.Get("Authorization")
	if h == "" {
		return "", "", false
	}
	sch, val, found := strings.Cut(h, " ")
	if !found {
		return "", "", false
	}
	return sch, strings.TrimSpace(val), true
}
