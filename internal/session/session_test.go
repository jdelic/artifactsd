package session

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newManager(t *testing.T) *Manager {
	t.Helper()
	key, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	return NewManager(key, "", true)
}

func TestEncodeDecode_RoundTrip(t *testing.T) {
	m := newManager(t)
	id := &Identity{Subject: "u1", Email: "a@example.com", Groups: []string{"staff"}, Expiry: time.Now().Add(time.Hour)}
	val, err := m.Encode(id)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := m.Decode(val)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got.Subject != "u1" || got.Email != "a@example.com" || len(got.Groups) != 1 {
		t.Errorf("round-trip mismatch: %+v", got)
	}
}

func TestDecode_RejectsTamper(t *testing.T) {
	m := newManager(t)
	val, _ := m.Encode(&Identity{Subject: "u1", Expiry: time.Now().Add(time.Hour)})
	// Flip a character in the payload.
	tampered := "x" + val[1:]
	if _, err := m.Decode(tampered); err == nil {
		t.Error("tampered cookie accepted")
	}
}

func TestDecode_RejectsForeignKey(t *testing.T) {
	m1 := newManager(t)
	m2 := newManager(t)
	val, _ := m1.Encode(&Identity{Subject: "u1", Expiry: time.Now().Add(time.Hour)})
	if _, err := m2.Decode(val); err == nil {
		t.Error("cookie signed by another key accepted")
	}
}

func TestDecode_RejectsExpired(t *testing.T) {
	m := newManager(t)
	val, _ := m.Encode(&Identity{Subject: "u1", Expiry: time.Now().Add(-time.Minute)})
	if _, err := m.Decode(val); err == nil {
		t.Error("expired session accepted")
	}
}

func TestCookieRoundTrip(t *testing.T) {
	m := newManager(t)
	id := &Identity{Subject: "u1", Expiry: time.Now().Add(time.Hour)}

	rec := httptest.NewRecorder()
	if err := m.SetCookie(rec, id); err != nil {
		t.Fatalf("SetCookie: %v", err)
	}
	c := rec.Result().Cookies()[0]
	if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode {
		t.Errorf("cookie flags wrong: %+v", c)
	}

	r := httptest.NewRequest(http.MethodGet, "/admin", http.NoBody)
	r.AddCookie(c)
	got, err := m.FromRequest(r)
	if err != nil || got.Subject != "u1" {
		t.Fatalf("FromRequest: %v (%+v)", err, got)
	}
}

func TestFromRequest_NoCookie(t *testing.T) {
	m := newManager(t)
	r := httptest.NewRequest(http.MethodGet, "/admin", http.NoBody)
	if _, err := m.FromRequest(r); err != ErrNoSession {
		t.Errorf("FromRequest no cookie = %v, want ErrNoSession", err)
	}
}

func TestCSRFToken_RoundTrip(t *testing.T) {
	m := newManager(t)
	id := &Identity{Subject: "u1", Expiry: time.Now().Add(time.Hour)}
	token := m.CSRFToken(id)
	if token == "" {
		t.Fatal("CSRFToken returned empty string")
	}
	if !m.VerifyCSRFToken(id, token) {
		t.Error("VerifyCSRFToken rejected its own token")
	}
}

func TestCSRFToken_RejectsTamperedToken(t *testing.T) {
	m := newManager(t)
	id := &Identity{Subject: "u1", Expiry: time.Now().Add(time.Hour)}
	token := m.CSRFToken(id)
	if m.VerifyCSRFToken(id, tamperFirstByte(token)) {
		t.Error("VerifyCSRFToken accepted a tampered token")
	}
}

// tamperFirstByte flips the token's first character to a value guaranteed to
// differ from the original, regardless of what that original character was
// (base64url's 64-character alphabet includes both 'x' and 'y', so a fixed
// replacement of "x" alone is occasionally a no-op when the token already
// starts with 'x').
func tamperFirstByte(s string) string {
	if s[0] == 'x' {
		return "y" + s[1:]
	}
	return "x" + s[1:]
}

func TestCSRFToken_RejectsDifferentIdentity(t *testing.T) {
	m := newManager(t)
	id1 := &Identity{Subject: "u1", Expiry: time.Now().Add(time.Hour)}
	id2 := &Identity{Subject: "u2", Expiry: id1.Expiry}
	token := m.CSRFToken(id1)
	if m.VerifyCSRFToken(id2, token) {
		t.Error("VerifyCSRFToken accepted a token minted for a different identity")
	}
}

func TestCSRFToken_DifferentKeysDiffer(t *testing.T) {
	m1 := newManager(t)
	m2 := newManager(t)
	id := &Identity{Subject: "u1", Expiry: time.Now().Add(time.Hour)}
	if m1.CSRFToken(id) == m2.CSRFToken(id) {
		t.Error("CSRFToken collided across independently generated keys")
	}
}
