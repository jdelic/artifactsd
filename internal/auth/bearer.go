package auth

import (
	"net/http"
	"strings"
)

// BearerVerifier accepts a request carrying any of a set of bearer tokens,
// matched by sha256 hash (design/06 §1.1). It is used for object-level
// `{type:bearer, token_hash}` auth entries; the management API uses the token
// store instead.
type BearerVerifier struct {
	hashes []string // sha256 hex of accepted tokens
}

// NewBearerVerifier builds a verifier accepting tokens whose sha256 hex hash is
// in hashes.
func NewBearerVerifier(hashes ...string) *BearerVerifier {
	return &BearerVerifier{hashes: hashes}
}

// Verify matches `Authorization: Bearer <token>` against the accepted hashes in
// constant time (no early exit on match). The subject is the token prefix.
func (v *BearerVerifier) Verify(r *http.Request) (bool, string) {
	scheme, token, ok := parseAuthHeader(r)
	if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" {
		return false, ""
	}
	presented := HashToken(token)
	matched := false
	for _, h := range v.hashes {
		if EqualHash(presented, h) {
			matched = true // keep scanning to avoid leaking which hash matched
		}
	}
	if !matched {
		return false, ""
	}
	return true, TokenPrefix(token)
}

// Scheme returns "Bearer".
func (v *BearerVerifier) Scheme() string { return "Bearer" }

// Challenge returns the WWW-Authenticate fragment.
func (v *BearerVerifier) Challenge() string { return "Bearer" }
