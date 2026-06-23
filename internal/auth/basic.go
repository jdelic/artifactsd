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
	pwOK, _ := VerifyPassword(pass, hashToCheck)

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
