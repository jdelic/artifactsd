package admin

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestMetadata_StructuredFieldsPreserveAuthSetViaRaw(t *testing.T) {
	ha := newFullHarness(t)
	form := url.Values{"name": {"demo-run"}}
	doReq(t, ha, http.MethodPost, "/collections", strings.NewReader(form.Encode()), "application/x-www-form-urlencoded")

	// Set a bearer token via the raw JSON editor.
	raw := url.Values{"raw": {`{"access":{"auth":[{"type":"bearer","token":"s3cr3t"}]}}`}}
	rec := doReq(t, ha, http.MethodPost, "/collections/demo-run/meta/raw", strings.NewReader(raw.Encode()), "application/x-www-form-urlencoded")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "token configured") {
		t.Fatalf("save raw = %d, body %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "s3cr3t") {
		t.Fatalf("raw view echoed back a cleartext secret: %s", rec.Body.String())
	}

	// A structured-fields save (e.g. setting visibility) must not drop the
	// bearer auth entry set above.
	fields := url.Values{"visibility": {"public"}}
	rec = doReq(t, ha, http.MethodPost, "/collections/demo-run/meta/fields", strings.NewReader(fields.Encode()), "application/x-www-form-urlencoded")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "token configured") {
		t.Fatalf("structured save dropped auth entry: %d, body %s", rec.Code, rec.Body.String())
	}

	rec = doReq(t, ha, http.MethodGet, "/collections/demo-run/meta", http.NoBody, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `selected>public`) {
		t.Fatalf("reload meta = %d, body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "token configured") {
		t.Fatalf("reload meta dropped auth entry: %s", rec.Body.String())
	}
}

func TestMetadata_NodeLevel_InvalidHeaderSurfacesError(t *testing.T) {
	ha := newFullHarness(t)
	form := url.Values{"name": {"demo-run"}}
	doReq(t, ha, http.MethodPost, "/collections", strings.NewReader(form.Encode()), "application/x-www-form-urlencoded")
	uploadMultipart(t, ha, "/collections/demo-run/upload", map[string]string{"dir": ""}, "report.txt", "hello")

	fields := url.Values{"headers": {"not-a-valid-header-line"}}
	rec := doReq(t, ha, http.MethodPost, "/collections/demo-run/meta/fields?path=report.txt", strings.NewReader(fields.Encode()), "application/x-www-form-urlencoded")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "invalid headers") {
		t.Fatalf("expected inline header error, got %d: %s", rec.Code, rec.Body.String())
	}

	fields = url.Values{"content_type": {"text/plain"}, "headers": {"Cache-Control: no-store"}}
	rec = doReq(t, ha, http.MethodPost, "/collections/demo-run/meta/fields?path=report.txt", strings.NewReader(fields.Encode()), "application/x-www-form-urlencoded")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "saved") {
		t.Fatalf("valid header save = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestShares_CreateListRevoke(t *testing.T) {
	ha := newFullHarness(t)
	form := url.Values{"name": {"demo-run"}}
	doReq(t, ha, http.MethodPost, "/collections", strings.NewReader(form.Encode()), "application/x-www-form-urlencoded")

	rec := doReq(t, ha, http.MethodGet, "/collections/demo-run/shares", http.NoBody, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "No shares yet") {
		t.Fatalf("initial shares = %d: %s", rec.Code, rec.Body.String())
	}

	shareForm := url.Values{"target_path": {""}, "expires_in": {"7d"}}
	rec = doReq(t, ha, http.MethodPost, "/collections/demo-run/shares", strings.NewReader(shareForm.Encode()), "application/x-www-form-urlencoded")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "/s/") {
		t.Fatalf("create share = %d: %s", rec.Code, rec.Body.String())
	}
	shareID := extractShareID(rec.Body.String())
	if shareID == "" {
		t.Fatalf("could not find share id in response: %s", rec.Body.String())
	}

	rec = doReq(t, ha, http.MethodPost, "/collections/demo-run/shares/"+shareID+"/revoke", http.NoBody, "")
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), shareID) {
		t.Fatalf("revoke share = %d: %s", rec.Code, rec.Body.String())
	}
}

func extractShareID(body string) string {
	const marker = `/s/`
	i := strings.Index(body, marker)
	if i < 0 {
		return ""
	}
	rest := body[i+len(marker):]
	j := strings.IndexAny(rest, "/\"<")
	if j < 0 {
		return ""
	}
	return rest[:j]
}

func TestTokens_CreateListRevoke(t *testing.T) {
	ha := newFullHarness(t)

	rec := doReq(t, ha, http.MethodGet, "/tokens", http.NoBody, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "No tokens yet") {
		t.Fatalf("initial tokens = %d: %s", rec.Code, rec.Body.String())
	}

	form := url.Values{"name": {"ci-runner"}, "scopes": {"collections:read"}}
	rec = doReq(t, ha, http.MethodPost, "/tokens", strings.NewReader(form.Encode()), "application/x-www-form-urlencoded")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "shown once") {
		t.Fatalf("create token = %d: %s", rec.Code, rec.Body.String())
	}

	rec = doReq(t, ha, http.MethodGet, "/tokens", http.NoBody, "")
	if !strings.Contains(rec.Body.String(), "ci-runner") || !strings.Contains(rec.Body.String(), "active") {
		t.Fatalf("token list missing entry: %s", rec.Body.String())
	}

	// No scopes selected is a validation error, not a 500.
	bad := url.Values{"name": {"no-scope"}}
	rec = doReq(t, ha, http.MethodPost, "/tokens", strings.NewReader(bad.Encode()), "application/x-www-form-urlencoded")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "at least one scope") {
		t.Fatalf("expected scope validation error, got %d: %s", rec.Code, rec.Body.String())
	}

	rec = doReq(t, ha, http.MethodPost, "/tokens/1/revoke", http.NoBody, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "revoked") {
		t.Fatalf("revoke token = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAuditLog_RecordsAndFilters(t *testing.T) {
	ha := newFullHarness(t)
	form := url.Values{"name": {"demo-run"}}
	doReq(t, ha, http.MethodPost, "/collections", strings.NewReader(form.Encode()), "application/x-www-form-urlencoded")

	rec := doReq(t, ha, http.MethodGet, "/audit", http.NoBody, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "collection.create") {
		t.Fatalf("audit log missing collection.create: %d: %s", rec.Code, rec.Body.String())
	}

	rec = doReq(t, ha, http.MethodGet, "/audit?action=token.create", http.NoBody, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "No matching entries") {
		t.Fatalf("filtered audit log should be empty: %s", rec.Body.String())
	}
}

// TestMetadata_SandboxToggle covers artifactsd-a8p: content.sandbox is settable
// from the structured fields form (tri-state) and round-trips to JSON, with a
// cautionary note when off.
func TestMetadata_SandboxToggle(t *testing.T) {
	ha := newFullHarness(t)
	doReq(t, ha, http.MethodPost, "/collections", strings.NewReader(url.Values{"name": {"demo-run"}}.Encode()), "application/x-www-form-urlencoded")

	fields := url.Values{"sandbox": {"false"}}
	rec := doReq(t, ha, http.MethodPost, "/collections/demo-run/meta/fields", strings.NewReader(fields.Encode()), "application/x-www-form-urlencoded")
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, "saved") {
		t.Fatalf("save sandbox = %d: %s", rec.Code, body)
	}
	if !strings.Contains(body, "selected>off (trusted)") {
		t.Errorf("sandbox=off not persisted/selected: %s", body)
	}
	if !strings.Contains(body, "Sandbox is off") {
		t.Errorf("missing sandbox-off warning: %s", body)
	}
	// Reload from storage: the select must come back selected on "off".
	rec = doReq(t, ha, http.MethodGet, "/collections/demo-run/meta", http.NoBody, "")
	if !strings.Contains(rec.Body.String(), "selected>off (trusted)") {
		t.Errorf("sandbox=off did not persist across reload: %s", rec.Body.String())
	}
}

// TestMetadata_ExpiryEnableAndBanner covers artifactsd-a0f: a mechanism is only
// saved when its enable checkbox is on, and an active expiry shows the banner.
func TestMetadata_ExpiryEnableAndBanner(t *testing.T) {
	ha := newFullHarness(t)
	doReq(t, ha, http.MethodPost, "/collections", strings.NewReader(url.Values{"name": {"demo-run"}}.Encode()), "application/x-www-form-urlencoded")
	uploadMultipart(t, ha, "/collections/demo-run/upload", map[string]string{"dir": ""}, "report.txt", "hello")

	// Enabled: value is honored and the effective-expiry banner appears.
	on := url.Values{"delete_after_creation_enabled": {"on"}, "delete_after_creation": {"30d"}}
	rec := doReq(t, ha, http.MethodPost, "/collections/demo-run/meta/fields?path=report.txt", strings.NewReader(on.Encode()), "application/x-www-form-urlencoded")
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, "saved") {
		t.Fatalf("save expiry = %d: %s", rec.Code, body)
	}
	if !strings.Contains(body, "set to expire on") {
		t.Errorf("missing effective-expiry banner: %s", body)
	}

	// A value submitted WITHOUT its enable checkbox must be ignored (off).
	off := url.Values{"delete_after_creation": {"99d"}} // no _enabled
	doReq(t, ha, http.MethodPost, "/collections/demo-run/meta/fields?path=report.txt", strings.NewReader(off.Encode()), "application/x-www-form-urlencoded")
	rec = doReq(t, ha, http.MethodGet, "/collections/demo-run/meta?path=report.txt", http.NoBody, "")
	if strings.Contains(rec.Body.String(), "99d") {
		t.Errorf("expiry saved despite its enable checkbox being off: %s", rec.Body.String())
	}
}

// TestMetadata_RawErrorAndPolicyRename covers artifactsd-3aw and 23u: the
// JSON-policy panel is renamed and links to the explainer, and an invalid JSON
// document is rejected (not saved) while preserving the operator's input.
func TestMetadata_RawErrorAndPolicyRename(t *testing.T) {
	ha := newFullHarness(t)
	doReq(t, ha, http.MethodPost, "/collections", strings.NewReader(url.Values{"name": {"demo-run"}}.Encode()), "application/x-www-form-urlencoded")

	rec := doReq(t, ha, http.MethodGet, "/collections/demo-run/meta", http.NoBody, "")
	body := rec.Body.String()
	if !strings.Contains(body, "JSON Policy document") {
		t.Errorf("Raw JSON panel not renamed: %s", body)
	}
	if !strings.Contains(body, "03-metadata-policy.md") {
		t.Errorf("missing explainer link: %s", body)
	}

	bad := url.Values{"raw": {`{ not valid json`}}
	rec = doReq(t, ha, http.MethodPost, "/collections/demo-run/meta/raw", strings.NewReader(bad.Encode()), "application/x-www-form-urlencoded")
	body = rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("raw save = %d: %s", rec.Code, body)
	}
	if strings.Contains(body, "saved") {
		t.Errorf("invalid JSON should not have saved")
	}
	if !strings.Contains(body, "not valid json") {
		t.Errorf("operator's unsaved JSON text was not preserved")
	}
}

// TestQuickShare covers artifactsd-jaj: the one-click Share endpoint mints a
// share link for a file, a folder, or the whole collection, and rejects a
// non-existent path.
func TestQuickShare(t *testing.T) {
	ha := newFullHarness(t)
	doReq(t, ha, http.MethodPost, "/collections", strings.NewReader(url.Values{"name": {"demo-run"}}.Encode()), "application/x-www-form-urlencoded")
	uploadMultipart(t, ha, "/collections/demo-run/upload", map[string]string{"dir": ""}, "report.txt", "hi")
	uploadMultipart(t, ha, "/collections/demo-run/upload", map[string]string{"dir": "", "subdir": "reports"}, "data.txt", "x")

	for _, target := range []string{"report.txt", "reports", ""} {
		rec := doReq(t, ha, http.MethodPost, "/collections/demo-run/share", strings.NewReader(url.Values{"target_path": {target}}.Encode()), "application/x-www-form-urlencoded")
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "/s/") {
			t.Fatalf("quick share target=%q = %d: %s", target, rec.Code, rec.Body.String())
		}
	}

	// All three appear in the global shares overview.
	rec := doReq(t, ha, http.MethodGet, "/shares", http.NoBody, "")
	if n := strings.Count(rec.Body.String(), "/s/"); n < 3 {
		t.Errorf("global shares should list the 3 new shares, found %d /s/ links", n)
	}

	// Sharing a path that does not exist is rejected (no link minted).
	rec = doReq(t, ha, http.MethodPost, "/collections/demo-run/share", strings.NewReader(url.Values{"target_path": {"missing.txt"}}.Encode()), "application/x-www-form-urlencoded")
	if strings.Contains(rec.Body.String(), "/s/") {
		t.Errorf("a missing path should not produce a share link: %s", rec.Body.String())
	}
}
