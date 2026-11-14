// Package auth implements artifactsd's authentication primitives: argon2id
// password hashing (basic-auth / admin fallback) and sha256 token hashing +
// generation for bearer and API tokens (design/06 §1-2). Bearer/basic/OIDC
// request verifiers build on these in later tickets (design/14).
package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

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

// defaultParams are the design/06 §2 defaults: memory=64 MiB, time=2 passes,
// threads=4, 16-byte salt, 32-byte key. Basic-auth verification is low-traffic,
// so we favor memory hardness; costs are encoded per-hash and can be raised
// later without invalidating existing hashes.
var defaultParams = argon2idParams{
	memory:  64 * 1024,
	time:    2,
	threads: 4,
	keyLen:  32,
	saltLen: 16,
}

// ErrInvalidHash is returned when an encoded hash cannot be parsed or its cost
// parameters are out of bounds.
var ErrInvalidHash = errors.New("auth: invalid argon2id hash")

// Bounds on argon2id parameters accepted from stored hashes
// (artifactsd-m9e.9). Hashes come from metadata any write-scoped token can set,
// and each verification runs with the hash's own costs, so unbounded values let
// one unauthenticated request burn seconds of CPU and arbitrary memory.
const (
	minArgonMemory  = 8 * 1024   // KiB = 8 MiB
	maxArgonMemory  = 128 * 1024 // KiB = 128 MiB
	minArgonTime    = 1
	maxArgonTime    = 6
	minArgonThreads = 1
	maxArgonThreads = 8
	minArgonSalt    = 8
	maxArgonSalt    = 64
	minArgonKey     = 16
	maxArgonKey     = 64
)

// argon2Sem caps concurrent argon2 computations, so a burst of requests with
// Basic credentials cannot allocate unbounded memory before the auth-failure
// limiter sees them. Waiters queue. A variable so tests can shrink it.
var argon2Sem = make(chan struct{}, 4)

// argon2Runs counts argon2 computations (tests and diagnostics).
var argon2Runs atomic.Int64

// argon2Hook, when set (tests only), is called around every computation.
var argon2Hook func(start bool)

// setArgon2Concurrency replaces the semaphore (tests); it returns a restore
// function.
func setArgon2Concurrency(n int) (restore func()) {
	prev := argon2Sem
	argon2Sem = make(chan struct{}, n)
	return func() { argon2Sem = prev }
}

// idKey runs argon2.IDKey under the concurrency cap. It returns ctx's error if
// ctx ends while waiting for a slot.
func idKey(ctx context.Context, password, salt []byte, p argon2idParams) ([]byte, error) {
	sem := argon2Sem
	select {
	case sem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-sem }()
	if argon2Hook != nil {
		argon2Hook(true)
		defer argon2Hook(false)
	}
	argon2Runs.Add(1)
	return argon2.IDKey(password, salt, p.time, p.memory, p.threads, p.keyLen), nil
}

// ValidateHash checks that encoded is a well-formed argon2id PHC string whose
// parameters are within the accepted bounds (8–128 MiB memory, 1–6 passes,
// 1–8 threads, 8–64 byte salt, 16–64 byte key).
func ValidateHash(encoded string) error {
	_, _, _, err := decodeHash(encoded)
	return err
}

// HashPassword returns a PHC-formatted argon2id hash of password:
//
//	$argon2id$v=19$m=65536,t=2,p=4$<b64salt>$<b64hash>
func HashPassword(password string) (string, error) {
	return hashWithParams(password, defaultParams)
}

func hashWithParams(password string, p argon2idParams) (string, error) {
	salt := make([]byte, p.saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: generating salt: %w", err)
	}
	key, err := idKey(context.Background(), []byte(password), salt, p)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.memory, p.time, p.threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// VerifyPassword is VerifyPasswordContext without a deadline.
func VerifyPassword(password, encoded string) (bool, error) {
	return VerifyPasswordContext(context.Background(), password, encoded)
}

// VerifyPasswordContext reports whether password matches the PHC-encoded
// argon2id hash. Comparison is constant-time. A malformed or out-of-bounds hash
// returns ErrInvalidHash without running argon2. It waits for a slot under the
// global concurrency cap and returns false with ctx's error if ctx ends first.
func VerifyPasswordContext(ctx context.Context, password, encoded string) (bool, error) {
	p, salt, key, err := decodeHash(encoded)
	if err != nil {
		return false, err
	}
	candidate, err := idKey(ctx, []byte(password), salt, p)
	if err != nil {
		return false, err
	}
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
	if p.memory < minArgonMemory || p.memory > maxArgonMemory ||
		p.time < minArgonTime || p.time > maxArgonTime ||
		p.threads < minArgonThreads || p.threads > maxArgonThreads ||
		p.saltLen < minArgonSalt || p.saltLen > maxArgonSalt ||
		p.keyLen < minArgonKey || p.keyLen > maxArgonKey {
		return p, nil, nil, fmt.Errorf("%w: parameters out of bounds (m=%d KiB, t=%d, p=%d, salt=%d B, key=%d B)",
			ErrInvalidHash, p.memory, p.time, p.threads, p.saltLen, p.keyLen)
	}
	return p, salt, key, nil
}
