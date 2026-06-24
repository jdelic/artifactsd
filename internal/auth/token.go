package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
)

// TokenPrefixValue is the literal prefix on every management API token.
const TokenPrefixValue = "tok_"

// SharePrefixValue is the literal prefix on every share id (the unguessable
// capability in a /s/{share_id}/ URL, design/04 §6).
const SharePrefixValue = "shr_"

// tokenSecretBytes is the entropy of the random secret (256 bits, design/06 §1.1).
const tokenSecretBytes = 32

// shareSecretBytes is the entropy of a share id (128 bits is ample for an
// unguessable URL capability while keeping the id short).
const shareSecretBytes = 16

// prefixLen is how many leading characters are stored as token_prefix for
// display/lookup hinting (covers "tok_" + 8 secret chars).
const prefixLen = 12

// base62Alphabet is the digit set for token secrets.
const base62Alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// HashToken returns the lower-case hex sha256 of a token string. Bearer and API
// tokens are high-entropy, so a fast indexable hash is appropriate (design/06
// §1.1/§2) — argon2 is reserved for low-entropy passwords.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// EqualHash reports whether two hex digests are equal in constant time.
func EqualHash(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// VerifyToken reports whether a presented token matches a stored sha256 hex
// hash, in constant time.
func VerifyToken(token, storedHash string) bool {
	return EqualHash(HashToken(token), storedHash)
}

// GenerateToken mints a new management API token. It returns the full token
// (shown to the operator exactly once), its sha256 hash (to persist), and the
// display prefix. The secret carries 256 bits of CSPRNG entropy.
func GenerateToken() (token, hash, prefix string, err error) {
	secret, err := randomBase62(tokenSecretBytes)
	if err != nil {
		return "", "", "", err
	}
	token = TokenPrefixValue + secret
	return token, HashToken(token), TokenPrefix(token), nil
}

// GenerateShareID mints a new unguessable share id (design/04 §6). Possession of
// the id is the capability to reach the share, so it carries CSPRNG entropy and
// is never derived from the target.
func GenerateShareID() (string, error) {
	secret, err := randomBase62(shareSecretBytes)
	if err != nil {
		return "", err
	}
	return SharePrefixValue + secret, nil
}

// TokenPrefix returns the leading prefixLen characters of a token for display.
func TokenPrefix(token string) string {
	if len(token) <= prefixLen {
		return token
	}
	return token[:prefixLen]
}

// randomBase62 returns a base62 string encoding nbytes of CSPRNG entropy.
func randomBase62(nbytes int) (string, error) {
	buf := make([]byte, nbytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("auth: reading entropy: %w", err)
	}
	n := new(big.Int).SetBytes(buf)
	if n.Sign() == 0 {
		return "0", nil
	}

	base := big.NewInt(62)
	mod := new(big.Int)
	var sb strings.Builder
	for n.Sign() > 0 {
		n.DivMod(n, base, mod)
		sb.WriteByte(base62Alphabet[mod.Int64()])
	}
	// Digits were emitted least-significant first; reverse for canonical order.
	return reverse(sb.String()), nil
}

func reverse(s string) string {
	b := []byte(s)
	for i, j := 0, len(b)-1; i < j; i, j = i+1, j-1 {
		b[i], b[j] = b[j], b[i]
	}
	return string(b)
}
