package auth

import (
	"net/http"
	"testing"
)

func bearerReq(token string) *http.Request {
	r, _ := http.NewRequest(http.MethodGet, "/", http.NoBody)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	return r
}

func basicReq(user, pass string) *http.Request {
	r, _ := http.NewRequest(http.MethodGet, "/", http.NoBody)
	r.SetBasicAuth(user, pass)
	return r
}

func TestBearerVerifier(t *testing.T) {
	v := NewBearerVerifier(HashToken("good-token"), HashToken("also-good"))

	ok, subj := v.Verify(bearerReq("good-token"))
	if !ok {
		t.Fatal("valid bearer token rejected")
	}
	if subj != TokenPrefix("good-token") {
		t.Errorf("subject = %q, want token prefix", subj)
	}

	if ok, _ := v.Verify(bearerReq("bad-token")); ok {
		t.Error("invalid bearer token accepted")
	}
	if ok, _ := v.Verify(bearerReq("")); ok {
		t.Error("missing token accepted")
	}
	// Wrong scheme.
	r, _ := http.NewRequest(http.MethodGet, "/", http.NoBody)
	r.Header.Set("Authorization", "Basic Zm9vOmJhcg==")
	if ok, _ := v.Verify(r); ok {
		t.Error("basic header accepted by bearer verifier")
	}
}

func TestBasicVerifier(t *testing.T) {
	hash, err := HashPassword("hunter2")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	v := NewBasicVerifier("demo", hash, "")

	ok, subj := v.Verify(basicReq("demo", "hunter2"))
	if !ok || subj != "demo" {
		t.Errorf("valid basic creds: ok=%v subj=%q", ok, subj)
	}
	if ok, _ := v.Verify(basicReq("demo", "wrong")); ok {
		t.Error("wrong password accepted")
	}
	if ok, _ := v.Verify(basicReq("nobody", "hunter2")); ok {
		t.Error("wrong username accepted")
	}
	// No auth header.
	r, _ := http.NewRequest(http.MethodGet, "/", http.NoBody)
	if ok, _ := v.Verify(r); ok {
		t.Error("missing basic creds accepted")
	}
}

func TestBasicVerifier_Challenge(t *testing.T) {
	v := NewBasicVerifier("demo", "x", "")
	if got := v.Challenge(); got != `Basic realm="artifactsd"` {
		t.Errorf("Challenge = %q", got)
	}
	v2 := NewBasicVerifier("demo", "x", "myrealm")
	if got := v2.Challenge(); got != `Basic realm="myrealm"` {
		t.Errorf("Challenge custom realm = %q", got)
	}
}

func TestAuthenticate_AnyOf(t *testing.T) {
	pw, _ := HashPassword("pw")
	vs := []Verifier{
		NewBearerVerifier(HashToken("tk")),
		NewBasicVerifier("u", pw, ""),
	}

	// Matches the bearer verifier.
	ok, subj, scheme := Authenticate(vs, bearerReq("tk"))
	if !ok || scheme != "Bearer" || subj != TokenPrefix("tk") {
		t.Errorf("bearer any-of: ok=%v scheme=%q subj=%q", ok, scheme, subj)
	}

	// Matches the basic verifier.
	ok, subj, scheme = Authenticate(vs, basicReq("u", "pw"))
	if !ok || scheme != "Basic" || subj != "u" {
		t.Errorf("basic any-of: ok=%v scheme=%q subj=%q", ok, scheme, subj)
	}

	// Matches neither.
	if ok, _, _ := Authenticate(vs, bearerReq("nope")); ok {
		t.Error("unauthenticated request accepted")
	}

	// Empty verifier list → not authenticated (policy treats empty as public).
	if ok, _, _ := Authenticate(nil, bearerReq("tk")); ok {
		t.Error("empty verifier list reported authenticated")
	}
}

func TestComposeChallenge(t *testing.T) {
	pw, _ := HashPassword("pw")
	vs := []Verifier{
		NewBearerVerifier("h"),
		NewBasicVerifier("u", pw, ""),
		NewBearerVerifier("h2"), // duplicate "Bearer" challenge collapses
	}
	got := ComposeChallenge(vs)
	want := `Bearer, Basic realm="artifactsd"`
	if got != want {
		t.Errorf("ComposeChallenge = %q, want %q", got, want)
	}
}
