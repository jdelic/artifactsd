package meta

import (
	"strings"
	"testing"

	"github.com/jdelic/artifactsd/internal/auth"
)

func strp(s string) *string { return &s }

// TestFromJSON_CanonicalizesHeaderKeys verifies the parse-time normalization
// (artifactsd-lw4.1): stored documents always hold HTTP-canonical header keys so
// they agree with the resolved/served form and can never collide after
// resolution.
func TestFromJSON_CanonicalizesHeaderKeys(t *testing.T) {
	d, err := FromJSON([]byte(`{"content":{"headers":{"cache-control":"no-store","x-custom-thing":"v"}}}`))
	if err != nil {
		t.Fatalf("FromJSON: %v", err)
	}
	h := d.Content.Headers
	if h["Cache-Control"] != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store (key should be canonicalized)", h["Cache-Control"])
	}
	if h["X-Custom-Thing"] != "v" {
		t.Errorf("X-Custom-Thing = %q, want v", h["X-Custom-Thing"])
	}
	if _, ok := h["cache-control"]; ok {
		t.Error("raw lower-case key should not survive parsing")
	}
}

// TestFromJSON_HeaderKeyCollisionCollapses verifies that two raw keys that
// canonicalize to the same name collapse to a single deterministic entry rather
// than producing a non-deterministic collision at resolution time.
func TestFromJSON_HeaderKeyCollisionCollapses(t *testing.T) {
	d, err := FromJSON([]byte(`{"content":{"headers":{"Cache-Control":"first","cache-control":"second"}}}`))
	if err != nil {
		t.Fatalf("FromJSON: %v", err)
	}
	h := d.Content.Headers
	if len(h) != 1 {
		t.Fatalf("headers = %v, want a single collapsed entry", h)
	}
	// "cache-control" sorts after "Cache-Control" (lower-case > upper-case in
	// ASCII), so the last-in-sorted-order value wins, deterministically.
	if h["Cache-Control"] != "second" {
		t.Errorf("collapsed value = %q, want %q (lexicographically-last raw key wins)", h["Cache-Control"], "second")
	}
}

func TestFromJSON_RoundTrip(t *testing.T) {
	in := `{"access":{"visibility":"public","directory_listing":true},"crawl":{"indexable":false}}`
	d, err := FromJSON([]byte(in))
	if err != nil {
		t.Fatalf("FromJSON: %v", err)
	}
	if d.Access == nil || d.Access.Visibility == nil || *d.Access.Visibility != "public" {
		t.Fatalf("visibility not parsed: %+v", d.Access)
	}
	if d.Access.DirectoryListing == nil || !*d.Access.DirectoryListing {
		t.Error("directory_listing not parsed")
	}
	if d.Crawl == nil || d.Crawl.Indexable == nil || *d.Crawl.Indexable {
		t.Error("crawl.indexable not parsed as false")
	}

	out, err := d.JSON()
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if !strings.Contains(string(out), `"visibility":"public"`) {
		t.Errorf("re-serialized JSON missing visibility: %s", out)
	}
}

func TestFromJSON_EmptyAndUnknownFields(t *testing.T) {
	d, err := FromJSON(nil)
	if err != nil || d == nil {
		t.Fatalf("empty FromJSON: %v", err)
	}
	if _, err := FromJSON([]byte(`{"bogus":1}`)); err == nil {
		t.Error("expected error on unknown field")
	}
}

func TestFromYAML(t *testing.T) {
	y := `
access:
  visibility: protected
  auth:
    - type: basic
      username: demo
      password: hunter2
expiry:
  delete_after_creation: 7d
`
	d, err := FromYAML([]byte(y))
	if err != nil {
		t.Fatalf("FromYAML: %v", err)
	}
	if d.Access.Auth == nil || len(*d.Access.Auth) != 1 || (*d.Access.Auth)[0].Username != "demo" {
		t.Fatalf("auth not parsed: %+v", d.Access)
	}
	if d.Expiry == nil || d.Expiry.DeleteAfterCreation == nil || *d.Expiry.DeleteAfterCreation != "7d" {
		t.Error("expiry not parsed")
	}
}

func TestUnsetVsZero(t *testing.T) {
	// directory_listing absent → nil (inherit); set false → non-nil false.
	d1, _ := FromJSON([]byte(`{"access":{}}`))
	if d1.Access.DirectoryListing != nil {
		t.Error("absent directory_listing should be nil")
	}
	d2, _ := FromJSON([]byte(`{"access":{"directory_listing":false}}`))
	if d2.Access.DirectoryListing == nil || *d2.Access.DirectoryListing {
		t.Error("explicit false directory_listing should be non-nil false")
	}

	// auth absent → nil (inherit); empty list → non-nil empty (no auth).
	d3, _ := FromJSON([]byte(`{"access":{}}`))
	if d3.Access.Auth != nil {
		t.Error("absent auth should be nil")
	}
	d4, _ := FromJSON([]byte(`{"access":{"auth":[]}}`))
	if d4.Access.Auth == nil || len(*d4.Access.Auth) != 0 {
		t.Error("explicit empty auth should be non-nil empty slice")
	}
}

func TestHashSecrets(t *testing.T) {
	d, _ := FromJSON([]byte(`{"access":{"auth":[
		{"type":"bearer","token":"sekret-token"},
		{"type":"basic","username":"u","password":"pw"}
	]}}`))
	if err := d.HashSecrets(); err != nil {
		t.Fatalf("HashSecrets: %v", err)
	}
	entries := *d.Access.Auth
	if entries[0].Token != "" || entries[0].TokenHash != auth.HashToken("sekret-token") {
		t.Errorf("bearer not hashed: %+v", entries[0])
	}
	if entries[1].Password != "" || !strings.HasPrefix(entries[1].PasswordHash, "$argon2id$") {
		t.Errorf("basic not hashed: %+v", entries[1])
	}
	if d.HasCleartextSecrets() {
		t.Error("cleartext secrets remain after HashSecrets")
	}
}

func TestRedacted(t *testing.T) {
	d, _ := FromJSON([]byte(`{"access":{"auth":[{"type":"bearer","token_hash":"abc"}]}}`))
	r := d.Redacted()
	e := (*r.Access.Auth)[0]
	if e.TokenHash != "" || e.Token != "" {
		t.Errorf("redacted entry still has secret material: %+v", e)
	}
	if !e.HasToken {
		t.Error("has_token presence flag not set")
	}
	// Original is untouched (deep copy).
	if (*d.Access.Auth)[0].TokenHash != "abc" {
		t.Error("Redacted mutated the original document")
	}

	out, _ := r.JSON()
	if !strings.Contains(string(out), `"has_token":true`) {
		t.Errorf("redacted JSON missing presence flag: %s", out)
	}
	if strings.Contains(string(out), "abc") {
		t.Errorf("redacted JSON leaked hash: %s", out)
	}
}

func TestValidate(t *testing.T) {
	ok, _ := FromJSON([]byte(`{"access":{"visibility":"public","auth":[{"type":"oidc","issuer":"https://idp","audience":"my-app","any_authenticated":true}]},"expiry":{"delete_after_creation":"7d"}}`))
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid doc rejected: %v", err)
	}

	// An OIDC entry without an audience is rejected: otherwise a token minted for
	// any other client at the same issuer would satisfy it (artifactsd-bhq).
	noAud, _ := FromJSON([]byte(`{"access":{"auth":[{"type":"oidc","issuer":"https://idp","any_authenticated":true}]}}`))
	if err := noAud.Validate(); err == nil || !strings.Contains(err.Error(), "audience") {
		t.Fatalf("missing-audience oidc entry not rejected: %v", err)
	}
	// The explicit, loud opt-out is accepted.
	skip, _ := FromJSON([]byte(`{"access":{"auth":[{"type":"oidc","issuer":"https://idp","insecure_skip_audience_check":true,"any_authenticated":true}]}}`))
	if err := skip.Validate(); err != nil {
		t.Fatalf("insecure_skip_audience_check opt-out rejected: %v", err)
	}

	bad := &Document{
		Access: &Access{
			Visibility: strp("translucent"),
			Auth: &[]AuthEntry{
				{Type: "bearer"},                      // missing token/hash
				{Type: "oidc", Issuer: "https://idp"}, // missing grant
				{Type: "weird"},                       // unknown type
			},
		},
		Expiry: &Expiry{DeleteAfterCreation: strp("soon")},
	}
	err := bad.Validate()
	if err == nil {
		t.Fatal("expected validation errors")
	}
	for _, want := range []string{"visibility", "bearer", "oidc", "unknown type", "duration"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}
}
