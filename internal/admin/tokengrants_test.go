package admin

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/jdelic/artifactsd/internal/store"
)

// TestTokens_RestrictedGrants: the tokens screen creates restricted tokens;
// each one's grants subpage lists, adds and removes grants; a collection's
// shares page lists its grants (artifactsd-m9e.29/.30, design/16 §7).
func TestTokens_RestrictedGrants(t *testing.T) {
	ha := newFullHarness(t)
	for _, n := range []string{"reports", "other"} {
		if err := ha.st.CreateCollection(context.Background(), &store.Collection{Name: n, Visibility: "public"}); err != nil {
			t.Fatal(err)
		}
	}
	post := func(path string, form url.Values) string {
		t.Helper()
		rec := doReq(t, ha, http.MethodPost, path, strings.NewReader(form.Encode()), "application/x-www-form-urlencoded")
		if rec.Code != http.StatusOK {
			t.Fatalf("POST %s = %d: %s", path, rec.Code, rec.Body)
		}
		return rec.Body.String()
	}

	// admin cannot be restricted.
	body := post("/tokens", url.Values{"name": {"bad"}, "restricted": {"on"}, "scopes": {"admin"}})
	if !strings.Contains(body, "admin tokens cannot be restricted") {
		t.Errorf("admin+restricted: %s", body)
	}
	// A restricted token needs no token-level scopes.
	body = post("/tokens", url.Values{"name": {"agent"}, "restricted": {"on"}})
	agent := tokenNamed(t, ha.st, "agent")
	if !agent.Restricted {
		t.Fatal("token not restricted")
	}
	id := strconv.FormatInt(agent.ID, 10)
	if !strings.Contains(body, "shown once") || !strings.Contains(body, `/admin/tokens/`+id+`/grants">add grants`) {
		t.Fatalf("create restricted should link its grants page: %s", body)
	}

	// The grants subpage starts empty and is a page of its own.
	rec := doReq(t, ha, http.MethodGet, "/tokens/"+id+"/grants", http.NoBody, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "No grants: this token reaches nothing") ||
		!strings.Contains(rec.Body.String(), `<option value="reports">`) {
		t.Fatalf("grants page = %d: %s", rec.Code, rec.Body)
	}
	// The collection picker is a searchable text input over a datalist
	// (artifactsd-0li), not a plain <select>.
	if page := rec.Body.String(); !strings.Contains(page, `list="grant-collections"`) ||
		!strings.Contains(page, `<datalist id="grant-collections">`) || strings.Contains(page, `<select name="collection"`) {
		t.Errorf("collection picker should be a searchable datalist input: %s", page)
	}
	body = post("/tokens/"+id+"/grants", url.Values{"collection": {"nope"}, "scopes": {"collections:read"}})
	if !strings.Contains(body, "unknown collection: nope") {
		t.Errorf("free-typed unknown collection: %s", body)
	}

	body = post("/tokens/"+id+"/grants", url.Values{"collection": {"reports"}, "scopes": {"collections:read", "shares:read"}})
	if !strings.Contains(body, `id="tokengrants-panel"`) || !strings.Contains(body, "reports") ||
		!strings.Contains(body, "collections:read, shares:read") {
		t.Errorf("after grant: %s", body)
	}
	// The tokens screen only summarizes and links the grants.
	rec = doReq(t, ha, http.MethodGet, "/tokens", http.NoBody, "")
	if !strings.Contains(rec.Body.String(), `/admin/tokens/`+id+`/grants">1 grant</a>`) ||
		strings.Contains(rec.Body.String(), "collections:read, shares:read") {
		t.Errorf("tokens screen should link, not inline, the grants: %s", rec.Body)
	}
	body = post("/tokens/"+id+"/grants", url.Values{"collection": {"other"}, "scopes": {"admin"}})
	if !strings.Contains(body, "admin cannot be granted") {
		t.Errorf("admin grant: %s", body)
	}
	body = post("/tokens/"+id+"/grants", url.Values{"collection": {"other"}})
	if !strings.Contains(body, "at least one scope") {
		t.Errorf("empty grant: %s", body)
	}

	// The collection's access (shares) page lists the tokens holding grants on it.
	rec = doReq(t, ha, http.MethodGet, "/collections/reports/shares", http.NoBody, "")
	if !strings.Contains(rec.Body.String(), `/admin/tokens/`+id+`/grants">agent</a>`) || !strings.Contains(rec.Body.String(), "collections:read, shares:read") {
		t.Errorf("collection page lacks the grant: %s", rec.Body)
	}

	body = post("/tokens/"+id+"/grants/reports/remove", url.Values{})
	grants, _ := ha.st.TokenGrants(context.Background(), agent.ID)
	if len(grants) != 0 || strings.Contains(body, "collections:read, shares:read") {
		t.Errorf("after remove: grants=%v body=%s", grants, body)
	}

	// Unrestricted tokens have no grants page, and grants on them are refused.
	post("/tokens", url.Values{"name": {"plain"}, "scopes": {"collections:read"}})
	plainGrants := "/tokens/" + strconv.FormatInt(tokenNamed(t, ha.st, "plain").ID, 10) + "/grants"
	rec = doReq(t, ha, http.MethodGet, plainGrants, http.NoBody, "")
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "not restricted") {
		t.Errorf("unrestricted grants page = %d: %s", rec.Code, rec.Body)
	}
	form := url.Values{"collection": {"reports"}, "scopes": {"collections:read"}}
	rec = doReq(t, ha, http.MethodPost, plainGrants, strings.NewReader(form.Encode()), "application/x-www-form-urlencoded")
	if rec.Code != http.StatusNotFound {
		t.Errorf("grant on unrestricted token = %d", rec.Code)
	}
	if rec = doReq(t, ha, http.MethodGet, "/tokens/999/grants", http.NoBody, ""); rec.Code != http.StatusNotFound {
		t.Errorf("unknown token grants page = %d", rec.Code)
	}

	entries, _ := ha.st.QueryAudit(context.Background(), store.AuditFilter{Action: "token.grant", Limit: 10})
	if len(entries) != 1 {
		t.Errorf("token.grant audit entries = %d, want 1", len(entries))
	}
	entries, _ = ha.st.QueryAudit(context.Background(), store.AuditFilter{Action: "token.ungrant", Limit: 10})
	if len(entries) != 1 {
		t.Errorf("token.ungrant audit entries = %d, want 1", len(entries))
	}
}

// TestNav_TokensAndAuditTopLevel: Tokens and Audit Log are top-level menu
// items; the old Tools section is gone (artifactsd-m9e.30).
func TestNav_TokensAndAuditTopLevel(t *testing.T) {
	ha := newFullHarness(t)
	for _, tc := range []struct{ path, active string }{
		{"/tokens", "Tokens"}, {"/audit", "Audit&nbsp;Log"},
	} {
		body := doReq(t, ha, http.MethodGet, tc.path, http.NoBody, "").Body.String()
		if strings.Contains(body, "Tools") || strings.Contains(body, `data-label="Tools"`) {
			t.Errorf("%s still shows Tools", tc.path)
		}
		if !strings.Contains(body, `href="/admin/tokens" data-shortcut="t"><u>T</u>okens</a>`) ||
			!strings.Contains(body, `href="/admin/audit" data-shortcut="a"><u>A</u>udit&nbsp;Log</a>`) {
			t.Errorf("%s nav lacks the Tokens/Audit Log items", tc.path)
		}
		if !strings.Contains(body, `nc-nav nc-nav-active" href="/admin/`+strings.ToLower(strings.Split(tc.active, "&")[0])) {
			t.Errorf("%s: %s not highlighted", tc.path, tc.active)
		}
	}
}

func tokenNamed(t *testing.T, st *store.Store, name string) *store.APIToken {
	t.Helper()
	toks, err := st.ListTokens(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, tk := range toks {
		if tk.Name == name {
			return tk
		}
	}
	t.Fatalf("no token %q", name)
	return nil
}

// TestTokens_RestrictAndAdminBadge: admin tokens are marked and offer only
// Revoke; other active unrestricted tokens can be restricted after the fact
// (artifactsd-m9e.31).
func TestTokens_RestrictAndAdminBadge(t *testing.T) {
	ha := newFullHarness(t)
	post := func(path string, form url.Values) string {
		t.Helper()
		rec := doReq(t, ha, http.MethodPost, path, strings.NewReader(form.Encode()), "application/x-www-form-urlencoded")
		if rec.Code != http.StatusOK {
			t.Fatalf("POST %s = %d: %s", path, rec.Code, rec.Body)
		}
		return rec.Body.String()
	}
	post("/tokens", url.Values{"name": {"root"}, "scopes": {"admin"}})
	post("/tokens", url.Values{"name": {"ci"}, "scopes": {"collections:write"}})
	root, ci := tokenNamed(t, ha.st, "root"), tokenNamed(t, ha.st, "ci")
	rootID, ciID := strconv.FormatInt(root.ID, 10), strconv.FormatInt(ci.ID, 10)

	rec := doReq(t, ha, http.MethodGet, "/tokens", http.NoBody, "")
	page := rec.Body.String()
	if !strings.Contains(page, `<span class="nc-admin">admin</span>`) {
		t.Errorf("admin token not marked: %s", page)
	}
	if !strings.Contains(page, `/tokens/`+ciID+`/restrict"`) || strings.Contains(page, `/tokens/`+rootID+`/restrict"`) {
		t.Errorf("Restrict should be offered for ci only: %s", page)
	}

	body := post("/tokens/"+ciID+"/restrict", nil)
	if !strings.Contains(body, `/admin/tokens/`+ciID+`/grants">no grants</a>`) {
		t.Errorf("after restrict: %s", body)
	}
	if got := tokenNamed(t, ha.st, "ci"); !got.Restricted || string(got.Scopes) != "[]" {
		t.Errorf("ci after restrict = %+v", got)
	}
	// Admin tokens cannot be restricted, even by a hand-made POST.
	body = post("/tokens/"+rootID+"/restrict", nil)
	if !strings.Contains(body, "admin tokens cannot be restricted") {
		t.Errorf("restrict admin: %s", body)
	}
	if tokenNamed(t, ha.st, "root").Restricted {
		t.Error("admin token was restricted")
	}
	body = post("/tokens/"+ciID+"/restrict", nil)
	if !strings.Contains(body, "already restricted") {
		t.Errorf("restrict twice: %s", body)
	}
}
