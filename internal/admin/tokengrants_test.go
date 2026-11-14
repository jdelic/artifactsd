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

// TestTokens_RestrictedGrants: the tokens screen creates restricted tokens,
// adds and removes grants, and a collection page lists its grants
// (artifactsd-m9e.29, design/16 §7).
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
	if !strings.Contains(body, "shown once") || !strings.Contains(body, "restricted") {
		t.Fatalf("create restricted: %s", body)
	}
	agent := tokenNamed(t, ha.st, "agent")
	if !agent.Restricted {
		t.Fatal("token not restricted")
	}
	id := strconv.FormatInt(agent.ID, 10)

	body = post("/tokens/"+id+"/grants", url.Values{"collection": {"reports"}, "scopes": {"collections:read", "shares:read"}})
	if !strings.Contains(body, "reports") || !strings.Contains(body, "collections:read, shares:read") {
		t.Errorf("after grant: %s", body)
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
	rec := doReq(t, ha, http.MethodGet, "/collections/reports/shares", http.NoBody, "")
	if !strings.Contains(rec.Body.String(), "agent") || !strings.Contains(rec.Body.String(), "collections:read, shares:read") {
		t.Errorf("collection page lacks the grant: %s", rec.Body)
	}

	body = post("/tokens/"+id+"/grants/reports/remove", url.Values{})
	grants, _ := ha.st.TokenGrants(context.Background(), agent.ID)
	if len(grants) != 0 || strings.Contains(body, "collections:read, shares:read") {
		t.Errorf("after remove: grants=%v body=%s", grants, body)
	}

	// Grants on an unrestricted token are refused.
	post("/tokens", url.Values{"name": {"plain"}, "scopes": {"collections:read"}})
	plain := tokenNamed(t, ha.st, "plain")
	body = post("/tokens/"+strconv.FormatInt(plain.ID, 10)+"/grants", url.Values{"collection": {"reports"}, "scopes": {"collections:read"}})
	if !strings.Contains(body, "grants require a restricted token") {
		t.Errorf("grant on unrestricted token: %s", body)
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
