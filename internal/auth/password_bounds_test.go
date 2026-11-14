package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	goodSalt = "c2FsdHNhbHRzYWx0c2FsdA"                      // 16 bytes
	goodKey  = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA" // 32 bytes
)

func phc(params string) string {
	return "$argon2id$v=19$" + params + "$" + goodSalt + "$" + goodKey
}

func TestValidateHash(t *testing.T) {
	good, err := HashPassword("pw")
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateHash(good); err != nil {
		t.Errorf("default hash rejected: %v", err)
	}
	cases := map[string]string{
		"memory too large": phc("m=262144,t=2,p=1"),
		"memory too small": phc("m=4096,t=2,p=1"),
		"time zero":        phc("m=65536,t=0,p=1"),
		"time too large":   phc("m=65536,t=40,p=1"),
		"threads zero":     phc("m=65536,t=2,p=0"),
		"threads too many": phc("m=65536,t=2,p=9"),
		"short salt":       "$argon2id$v=19$m=65536,t=2,p=1$c2FsdA$" + goodKey,
		"short key":        "$argon2id$v=19$m=65536,t=2,p=1$" + goodSalt + "$AAAA",
		"garbage":          "not-a-hash",
	}
	for name, h := range cases {
		if err := ValidateHash(h); err == nil {
			t.Errorf("%s: ValidateHash(%q) = nil, want error", name, h)
		}
	}
	if err := ValidateHash(phc("m=131072,t=6,p=8")); err != nil {
		t.Errorf("upper bounds rejected: %v", err)
	}
}

func TestVerifyPassword_OutOfRangeNeverRuns(t *testing.T) {
	before := argon2Runs.Load()
	for _, h := range []string{phc("m=65536,t=0,p=0"), phc("m=262144,t=40,p=1")} {
		ok, err := VerifyPassword("x", h)
		if ok || err == nil {
			t.Errorf("VerifyPassword(%q) = %v, %v; want false, ErrInvalidHash", h, ok, err)
		}
	}
	if n := argon2Runs.Load() - before; n != 0 {
		t.Errorf("out-of-range hashes ran argon2 %d times", n)
	}
}

func TestArgon2Semaphore_Serializes(t *testing.T) {
	restore := setArgon2Concurrency(1)
	defer restore()
	h, err := HashPassword("pw")
	if err != nil {
		t.Fatal(err)
	}
	var active, peak atomic.Int64
	argon2Hook = func(start bool) {
		if start {
			if n := active.Add(1); n > peak.Load() {
				peak.Store(n)
			}
		} else {
			active.Add(-1)
		}
	}
	defer func() { argon2Hook = nil }()
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = VerifyPassword("pw", h)
		}()
	}
	wg.Wait()
	if p := peak.Load(); p != 1 {
		t.Errorf("peak concurrent argon2 runs = %d, want 1", p)
	}
}

func TestVerifyPasswordContext_CanceledWhileWaiting(t *testing.T) {
	restore := setArgon2Concurrency(1)
	defer restore()
	h, _ := HashPassword("pw")
	argon2Sem <- struct{}{} // occupy the only slot
	defer func() { <-argon2Sem }()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	ok, err := VerifyPasswordContext(ctx, "pw", h)
	if ok || err == nil {
		t.Errorf("canceled wait = %v, %v; want false with an error", ok, err)
	}
}

func TestMultiBasicVerifier_OneRunPerRequest(t *testing.T) {
	var creds []BasicCredential
	var target string
	for i := range 10 {
		user := "user" + string(rune('a'+i))
		h, err := HashPassword("pw-" + user)
		if err != nil {
			t.Fatal(err)
		}
		creds = append(creds, BasicCredential{Username: user, PasswordHash: h})
		if i == 7 {
			target = user
		}
	}
	v := NewMultiBasicVerifier(creds, "")
	_ = dummyHash() // computed once per process; not part of a request's cost
	run := func(user, pass string) (bool, int64) {
		r := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		r.SetBasicAuth(user, pass)
		before := argon2Runs.Load()
		ok, _ := v.Verify(r)
		return ok, argon2Runs.Load() - before
	}
	if ok, n := run(target, "pw-"+target); !ok || n != 1 {
		t.Errorf("matching user: ok=%v runs=%d, want true and 1", ok, n)
	}
	if ok, n := run(target, "wrong"); ok || n != 1 {
		t.Errorf("wrong password: ok=%v runs=%d, want false and 1", ok, n)
	}
	if ok, n := run("nobody", "x"); ok || n != 1 {
		t.Errorf("unknown user: ok=%v runs=%d, want false and 1 (dummy)", ok, n)
	}
	if !strings.HasPrefix(v.Challenge(), "Basic realm=") {
		t.Errorf("challenge = %q", v.Challenge())
	}
}

// TestMultiBasicVerifier_DuplicateUsernames: repeated usernames (e.g. in old
// stored documents) still cost one argon2 run per request; the first entry for
// a username is the one that counts (artifactsd-7tl.4).
func TestMultiBasicVerifier_DuplicateUsernames(t *testing.T) {
	_ = dummyHash()
	h1, _ := HashPassword("first")
	h2, _ := HashPassword("second")
	creds := make([]BasicCredential, 0, 20)
	creds = append(creds, BasicCredential{Username: "same", PasswordHash: h1})
	for range 19 {
		creds = append(creds, BasicCredential{Username: "same", PasswordHash: h2})
	}
	v := NewMultiBasicVerifier(creds, "")
	try := func(pass string) (bool, int64) {
		r := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		r.SetBasicAuth("same", pass)
		before := argon2Runs.Load()
		ok, _ := v.Verify(r)
		return ok, argon2Runs.Load() - before
	}
	if ok, n := try("wrong"); ok || n != 1 {
		t.Errorf("wrong password: ok=%v runs=%d, want false and 1", ok, n)
	}
	if ok, n := try("first"); !ok || n != 1 {
		t.Errorf("first entry's password: ok=%v runs=%d, want true and 1", ok, n)
	}
	if ok, _ := try("second"); ok {
		t.Error("a later duplicate entry was honored")
	}
}
