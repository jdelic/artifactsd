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

// TestMetadata_RawErrorScopedAndPolicyRename covers artifactsd-3aw and 23u: a
// raw-JSON error renders inside the JSON-policy section (not detached at top),
// the panel is renamed, and links to the explainer.
func TestMetadata_RawErrorScopedAndPolicyRename(t *testing.T) {
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
	if !strings.Contains(body, `class="nc-info"`) {
		t.Errorf("missing per-field info icons")
	}

	bad := url.Values{"raw": {`{ not valid json`}}
	rec = doReq(t, ha, http.MethodPost, "/collections/demo-run/meta/raw", strings.NewReader(bad.Encode()), "application/x-www-form-urlencoded")
	body = rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("raw save = %d: %s", rec.Code, body)
	}
	policyIdx := strings.Index(body, "JSON Policy document")
	if policyIdx < 0 || !strings.Contains(body[policyIdx:], "nc-error") {
		t.Errorf("raw error not rendered inside the JSON-policy section: %s", body)
	}
	if strings.Contains(body[:policyIdx], "nc-error") {
		t.Errorf("raw error leaked into an earlier section: %s", body[:policyIdx])
	}
	if !strings.Contains(body, "not valid json") {
		t.Errorf("operator's unsaved JSON text was not preserved")
	}
}
