package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestHashToken_MatchesSHA256(t *testing.T) {
	got := HashToken("tok_abc")
	want := sha256.Sum256([]byte("tok_abc"))
	if got != hex.EncodeToString(want[:]) {
		t.Errorf("HashToken = %s, want %s", got, hex.EncodeToString(want[:]))
	}
	if len(got) != 64 {
		t.Errorf("hash length = %d, want 64", len(got))
	}
}

func TestVerifyToken(t *testing.T) {
	h := HashToken("secret-token")
	if !VerifyToken("secret-token", h) {
		t.Error("correct token did not verify")
	}
	if VerifyToken("wrong-token", h) {
		t.Error("wrong token verified")
	}
}

func TestEqualHash(t *testing.T) {
	if !EqualHash("abcd", "abcd") {
		t.Error("equal hashes reported unequal")
	}
	if EqualHash("abcd", "abce") {
		t.Error("unequal hashes reported equal")
	}
}

func TestGenerateToken(t *testing.T) {
	token, hash, prefix, err := GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	if !strings.HasPrefix(token, TokenPrefixValue) {
		t.Errorf("token %q lacks %q prefix", token, TokenPrefixValue)
	}
	if hash != HashToken(token) {
		t.Errorf("returned hash does not match HashToken(token)")
	}
	if prefix != TokenPrefix(token) || len(prefix) != prefixLen {
		t.Errorf("prefix = %q (len %d), want %q", prefix, len(prefix), TokenPrefix(token))
	}
	if !VerifyToken(token, hash) {
		t.Error("generated token does not verify against its hash")
	}

	// Tokens must be unique and carry real entropy.
	seen := map[string]bool{token: true}
	for i := 0; i < 100; i++ {
		tk, _, _, err := GenerateToken()
		if err != nil {
			t.Fatalf("GenerateToken iter %d: %v", i, err)
		}
		if seen[tk] {
			t.Fatalf("duplicate token generated: %q", tk)
		}
		seen[tk] = true
		// 256 bits in base62 is ~43 chars; require a healthy minimum.
		secret := strings.TrimPrefix(tk, TokenPrefixValue)
		if len(secret) < 32 {
			t.Errorf("token secret too short (%d chars): %q", len(secret), tk)
		}
	}
}

func TestTokenPrefix_Short(t *testing.T) {
	if got := TokenPrefix("tok_"); got != "tok_" {
		t.Errorf("TokenPrefix short = %q, want tok_", got)
	}
}

func TestArgon2Params_Updated(t *testing.T) {
	// Reconciliation guard: design/06 §2 mandates t=2.
	h, err := HashPassword("x")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !strings.Contains(h, "t=2") {
		t.Errorf("argon2id hash %q does not use t=2", h)
	}
}
