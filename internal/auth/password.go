// Package auth implements artifactsd's authentication primitives: argon2id
// password hashing for the admin basic fallback, plus (in later tickets)
// bearer/basic/OIDC verification and API-token hashing (design/06, design/14).
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// argon2idParams holds the cost parameters encoded into every hash so that
// verification is self-describing and the costs can be raised over time without
// invalidating existing hashes.
type argon2idParams struct {
	memory  uint32 // KiB
	time    uint32 // iterations
	threads uint8
	keyLen  uint32
	saltLen uint32
}

// defaultParams are sane interactive-login defaults (~64 MiB, 1 pass). The
// admin basic fallback is low-traffic, so we favor memory hardness.
var defaultParams = argon2idParams{
	memory:  64 * 1024,
	time:    1,
	threads: 4,
	keyLen:  32,
	saltLen: 16,
}

// ErrInvalidHash is returned when an encoded hash cannot be parsed.
var ErrInvalidHash = errors.New("auth: invalid argon2id hash")

// HashPassword returns a PHC-formatted argon2id hash of password:
//
//	$argon2id$v=19$m=65536,t=1,p=4$<b64salt>$<b64hash>
func HashPassword(password string) (string, error) {
	return hashWithParams(password, defaultParams)
}

func hashWithParams(password string, p argon2idParams) (string, error) {
	salt := make([]byte, p.saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: generating salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, p.time, p.memory, p.threads, p.keyLen)
	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.memory, p.time, p.threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// VerifyPassword reports whether password matches the PHC-encoded argon2id
// hash. Comparison is constant-time. A malformed hash returns ErrInvalidHash.
func VerifyPassword(password, encoded string) (bool, error) {
	p, salt, key, err := decodeHash(encoded)
	if err != nil {
		return false, err
	}
	candidate := argon2.IDKey([]byte(password), salt, p.time, p.memory, p.threads, p.keyLen)
	if subtle.ConstantTimeEq(int32(len(candidate)), int32(len(key))) == 0 {
		return false, nil
	}
	return subtle.ConstantTimeCompare(candidate, key) == 1, nil
}

func decodeHash(encoded string) (p argon2idParams, salt, key []byte, err error) {
	parts := strings.Split(encoded, "$")
	// ["", "argon2id", "v=19", "m=..,t=..,p=..", "<salt>", "<hash>"]
	if len(parts) != 6 || parts[1] != "argon2id" {
		return p, nil, nil, ErrInvalidHash
	}

	var version int
	if _, e := fmt.Sscanf(parts[2], "v=%d", &version); e != nil {
		return p, nil, nil, ErrInvalidHash
	}
	if version != argon2.Version {
		return p, nil, nil, fmt.Errorf("%w: unsupported version %d", ErrInvalidHash, version)
	}
	if _, e := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.memory, &p.time, &p.threads); e != nil {
		return p, nil, nil, ErrInvalidHash
	}

	salt, err = base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return p, nil, nil, ErrInvalidHash
	}
	key, err = base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return p, nil, nil, ErrInvalidHash
	}
	p.saltLen = uint32(len(salt))
	p.keyLen = uint32(len(key))
	return p, salt, key, nil
}
