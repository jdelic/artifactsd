package auth

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"sync"
)

// DefaultRealm is the WWW-Authenticate realm for basic auth.
const DefaultRealm = "artifactsd"

// BasicVerifier accepts HTTP Basic credentials for a single username whose
// password is stored as an argon2id hash (design/06 §1.2). To avoid a username
// enumeration timing oracle, it always performs one argon2id verification —
// against a dummy hash when the username does not match.
type BasicVerifier struct {
	username     string
	passwordHash string
	realm        string
}

// NewBasicVerifier builds a basic-auth verifier. An empty realm uses DefaultRealm.
func NewBasicVerifier(username, passwordHash, realm string) *BasicVerifier {
	if realm == "" {
		realm = DefaultRealm
	}
	return &BasicVerifier{username: username, passwordHash: passwordHash, realm: realm}
}

// Verify matches HTTP Basic credentials, constant-time on the username and with
// a dummy argon2id verify on username miss.
func (v *BasicVerifier) Verify(r *http.Request) (bool, string) {
	user, pass, ok := r.BasicAuth()
	if !ok {
		return false, ""
	}
	userMatch := subtle.ConstantTimeCompare([]byte(user), []byte(v.username)) == 1

	hashToCheck := v.passwordHash
	if !userMatch {
		hashToCheck = dummyHash()
	}
	pwOK, _ := VerifyPasswordContext(r.Context(), pass, hashToCheck)

	if userMatch && pwOK {
		return true, v.username
	}
	return false, ""
}

// Scheme returns "Basic".
func (v *BasicVerifier) Scheme() string { return "Basic" }

// Challenge returns `Basic realm="..."`.
func (v *BasicVerifier) Challenge() string {
	return fmt.Sprintf("Basic realm=%q", v.realm)
}

// dummyHash is a valid argon2id hash of an unknowable password, computed once,
// used to keep username-miss verification time comparable to a hit.
var dummyHash = sync.OnceValue(func() string {
	secret, err := randomBase62(tokenSecretBytes)
	if err != nil {
		secret = "artifactsd-dummy-password"
	}
	h, err := HashPassword(secret)
	if err != nil {
		// Should never happen; fall back to a static well-formed hash so Verify
		// still runs a real argon2 comparison.
		h, _ = HashPassword("artifactsd-dummy-password")
	}
	return h
})

// BasicCredential is one accepted username/argon2id-hash pair.
type BasicCredential struct {
	Username     string
	PasswordHash string
}

// MultiBasicVerifier accepts HTTP Basic credentials against several entries
// at the cost of ONE argon2 run per request regardless of how many entries an
// object lists (artifactsd-m9e.9): only entries whose username matches
// (constant-time) are verified, and a single dummy verification runs when none
// match, so a username miss costs the same as a hit.
type MultiBasicVerifier struct {
	creds []BasicCredential
	realm string
}

// NewMultiBasicVerifier builds a verifier over creds. An empty realm uses
// DefaultRealm. Only the first entry per username is kept, so a request costs
// one argon2 run even when a stored document repeats a username
// (artifactsd-7tl.4); new documents with duplicates are rejected on write.
func NewMultiBasicVerifier(creds []BasicCredential, realm string) *MultiBasicVerifier {
	if realm == "" {
		realm = DefaultRealm
	}
	seen := make(map[string]bool, len(creds))
	unique := make([]BasicCredential, 0, len(creds))
	for _, c := range creds {
		if seen[c.Username] {
			continue
		}
		seen[c.Username] = true
		unique = append(unique, c)
	}
	return &MultiBasicVerifier{creds: unique, realm: realm}
}

// Verify matches HTTP Basic credentials against the entries.
func (v *MultiBasicVerifier) Verify(r *http.Request) (bool, string) {
	user, pass, ok := r.BasicAuth()
	if !ok {
		return false, ""
	}
	// Usernames are unique (see NewMultiBasicVerifier): find the one match by
	// comparing against every entry, then run exactly one argon2 — against the
	// dummy hash on a miss so both cases cost the same.
	var match *BasicCredential
	for i := range v.creds {
		if subtle.ConstantTimeCompare([]byte(user), []byte(v.creds[i].Username)) == 1 && match == nil {
			match = &v.creds[i]
		}
	}
	if match == nil {
		_, _ = VerifyPasswordContext(r.Context(), pass, dummyHash())
		return false, ""
	}
	if ok, _ := VerifyPasswordContext(r.Context(), pass, match.PasswordHash); ok {
		return true, match.Username
	}
	return false, ""
}

// Scheme returns "Basic".
func (v *MultiBasicVerifier) Scheme() string { return "Basic" }

// Challenge returns `Basic realm="..."`.
func (v *MultiBasicVerifier) Challenge() string {
	return fmt.Sprintf("Basic realm=%q", v.realm)
}
