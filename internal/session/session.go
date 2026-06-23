// Package session implements artifactsd's admin session: a stateless,
// HMAC-signed cookie carrying the authenticated identity (design/14 §7). The
// signing key is generated at server start, so sessions are invalidated on
// restart — acceptable for a single-process admin UI.
package session

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// DefaultCookieName is the admin session cookie name.
const DefaultCookieName = "artifactsd_admin"

// ErrNoSession is returned when no (valid) session cookie is present.
var ErrNoSession = errors.New("session: no session")

// Identity is the authenticated admin principal stored in the session.
type Identity struct {
	Subject string    `json:"sub"`
	Email   string    `json:"email,omitempty"`
	Groups  []string  `json:"groups,omitempty"`
	Expiry  time.Time `json:"exp"`
}

// Manager signs, sets, and reads session cookies.
type Manager struct {
	key        []byte
	cookieName string
	secure     bool
}

// NewManager builds a session manager. secure sets the cookie Secure flag (true
// under TLS). An empty cookieName uses DefaultCookieName.
func NewManager(key []byte, cookieName string, secure bool) *Manager {
	if cookieName == "" {
		cookieName = DefaultCookieName
	}
	return &Manager{key: key, cookieName: cookieName, secure: secure}
}

// GenerateKey returns a 32-byte random signing key.
func GenerateKey() ([]byte, error) {
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		return nil, fmt.Errorf("session: generating key: %w", err)
	}
	return k, nil
}

// Sign returns an HMAC-signed, base64url cookie value for arbitrary bytes
// (base64url(payload).base64url(hmac)).
func (m *Manager) Sign(payload []byte) string {
	b64 := base64.RawURLEncoding.EncodeToString(payload)
	return b64 + "." + base64.RawURLEncoding.EncodeToString(m.mac(b64))
}

// Open verifies the signature and returns the payload bytes, or ErrNoSession.
func (m *Manager) Open(value string) ([]byte, error) {
	b64, sig, ok := strings.Cut(value, ".")
	if !ok {
		return nil, ErrNoSession
	}
	wantMAC, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil {
		return nil, ErrNoSession
	}
	if subtle.ConstantTimeCompare(wantMAC, m.mac(b64)) != 1 {
		return nil, ErrNoSession
	}
	payload, err := base64.RawURLEncoding.DecodeString(b64)
	if err != nil {
		return nil, ErrNoSession
	}
	return payload, nil
}

// Encode serializes and signs an identity into a cookie value.
func (m *Manager) Encode(id *Identity) (string, error) {
	payload, err := json.Marshal(id)
	if err != nil {
		return "", err
	}
	return m.Sign(payload), nil
}

// Decode verifies the signature and expiry, returning the identity.
func (m *Manager) Decode(value string) (*Identity, error) {
	payload, err := m.Open(value)
	if err != nil {
		return nil, err
	}
	var id Identity
	if err := json.Unmarshal(payload, &id); err != nil {
		return nil, ErrNoSession
	}
	if time.Now().After(id.Expiry) {
		return nil, ErrNoSession
	}
	return &id, nil
}

func (m *Manager) mac(b64 string) []byte {
	h := hmac.New(sha256.New, m.key)
	h.Write([]byte(b64))
	return h.Sum(nil)
}

// SetCookie writes the session cookie with the identity, expiring at id.Expiry.
func (m *Manager) SetCookie(w http.ResponseWriter, id *Identity) error {
	value, err := m.Encode(id)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     m.cookieName,
		Value:    value,
		Path:     "/",
		Expires:  id.Expiry,
		HttpOnly: true,
		Secure:   m.secure,
		SameSite: http.SameSiteLaxMode,
	})
	return nil
}

// Clear expires the session cookie.
func (m *Manager) Clear(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     m.cookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   m.secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// FromRequest reads and validates the session from the request, or ErrNoSession.
func (m *Manager) FromRequest(r *http.Request) (*Identity, error) {
	c, err := r.Cookie(m.cookieName)
	if err != nil {
		return nil, ErrNoSession
	}
	return m.Decode(c.Value)
}
