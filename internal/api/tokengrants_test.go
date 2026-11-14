package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/jdelic/artifactsd/internal/audit"
	"github.com/jdelic/artifactsd/internal/store"
)

// grantsFixture sets up two crawlable collections, alpha and beta, each with a
// file, a folder and a share, and returns their share ids.
func grantsFixture(t *testing.T, h *harness) (alphaShare, betaShare string) {
	t.Helper()
	share := func(c string) string {
		rec := h.do(t, h.token, http.MethodPost, "/api/v1/collections/"+c+"/shares", []byte(`{"target_path":""}`), nil)
		if rec.Code != http.StatusCreated {
			t.Fatalf("share %s = %d (%s)", c, rec.Code, rec.Body)
		}
		var sh struct {
			ShareID string `json:"share_id"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &sh)
		return sh.ShareID
	}
	for _, c := range []string{"alpha", "beta"} {
		mustCode(t, h.do(t, h.token, http.MethodPost, "/api/v1/collections",
			[]byte(`{"name":"`+c+`","metadata":{"crawl":{"crawlable":true}}}`), nil), http.StatusCreated)
		mustCode(t, h.do(t, h.token, http.MethodPut, "/api/v1/collections/"+c+"/files/f.txt", []byte("x"), nil), http.StatusCreated)
		mustCode(t, h.do(t, h.token, http.MethodPut, "/api/v1/collections/"+c+"/files/d/g.txt", []byte("y"), nil), http.StatusCreated)
	}
	return share("alpha"), share("beta")
}

func mustCode(t *testing.T, rec interface {
	Result() *http.Response
}, want int,
) {
	t.Helper()
	if got := rec.Result().StatusCode; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
}

type grantedToken struct {
	ID         int64       `json:"id"`
	Token      string      `json:"token"`
	Restricted bool        `json:"restricted"`
	Grants     []grantView `json:"grants"`
}

// mkGrantedToken creates a token through the API with the given request body.
func mkGrantedToken(t *testing.T, h *harness, body string) grantedToken {
	t.Helper()
	rec := h.do(t, h.token, http.MethodPost, "/api/v1/tokens", []byte(body), nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create token %s = %d (%s)", body, rec.Code, rec.Body)
	}
	var tok grantedToken
	if err := json.Unmarshal(rec.Body.Bytes(), &tok); err != nil {
		t.Fatalf("decode token: %v", err)
	}
	return tok
}

const allGrantScopes = `["collections:read","collections:write","collections:delete","shares:read","shares:write","shares:delete"]`

// collectionRoutes are every route that addresses a collection, for c.
func collectionRoutes(c, shareID string) []struct{ method, target, body string } {
	p := "/api/v1/collections/" + c
	return []struct{ method, target, body string }{
		{http.MethodGet, p, ""},
		{http.MethodGet, p + "/meta", ""},
		{http.MethodPatch, p + "/meta", `{}`},
		{http.MethodGet, p + "/files/f.txt", ""},
		{http.MethodGet, p + "/files/f.txt?meta", ""},
		{http.MethodPut, p + "/files/n.txt", "n"},
		{http.MethodPatch, p + "/files/f.txt?meta", `{}`},
		{http.MethodPost, p + "/uploads", `{"path":"big.bin"}`},
		{http.MethodGet, p + "/tree", ""},
		{http.MethodGet, p + "/folders/d?meta", ""},
		{http.MethodPatch, p + "/folders/d?meta", `{}`},
		{http.MethodGet, p + "/crawl", ""},
		{http.MethodGet, p + "/shares", ""},
		{http.MethodPost, p + "/shares", `{"target_path":""}`},
		{http.MethodGet, "/api/v1/shares/" + shareID, ""},
		{http.MethodPatch, "/api/v1/shares/" + shareID, `{}`},
		{http.MethodDelete, "/api/v1/shares/" + shareID, ""},
		{http.MethodDelete, p + "/files/f.txt", ""},
		{http.MethodDelete, p + "/folders/d?recursive=true", ""},
		{http.MethodDelete, p + "?force=true", ""},
	}
}

// TestTokenGrants_CollectionGate: a restricted token reaches only the
// collections it holds a grant for; everything else is a 404 indistinguishable
// from a missing collection (artifactsd-m9e.28, design/16 §4).
func TestTokenGrants_CollectionGate(t *testing.T) {
	h := newHarness(t)
	_, betaShare := grantsFixture(t, h)
	agent := mkGrantedToken(t, h, `{"name":"agent","restricted":true,"grants":[{"collection":"alpha","scopes":`+allGrantScopes+`}]}`)
	if !agent.Restricted || len(agent.Grants) != 1 || agent.Grants[0].Collection != "alpha" {
		t.Fatalf("created token = %+v, want restricted with one grant on alpha", agent)
	}

	for _, rt := range collectionRoutes("beta", betaShare) {
		var body []byte
		if rt.body != "" {
			body = []byte(rt.body)
		}
		rec := h.do(t, agent.Token, rt.method, rt.target, body, nil)
		if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "beta") {
			t.Errorf("%s %s = %d (%s), want 404 without naming the collection", rt.method, rt.target, rec.Code, rec.Body)
		}
	}
	// Nothing on beta was touched.
	mustCode(t, h.do(t, h.token, http.MethodGet, "/api/v1/collections/beta/files/f.txt", nil, nil), http.StatusOK)
	mustCode(t, h.do(t, h.token, http.MethodGet, "/api/v1/shares/"+betaShare, nil, nil), http.StatusOK)

	// A missing collection answers the same way.
	rec := h.do(t, agent.Token, http.MethodGet, "/api/v1/collections/nope", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("missing collection = %d", rec.Code)
	}

	// The granted collection works on every route, the destructive ones last.
	alphaShare := ""
	for _, rt := range collectionRoutes("alpha", "") {
		target := rt.target
		if strings.HasSuffix(target, "/shares/") {
			if alphaShare == "" {
				rec := h.do(t, agent.Token, http.MethodPost, "/api/v1/collections/alpha/shares", []byte(`{"target_path":""}`), nil)
				var sh struct {
					ShareID string `json:"share_id"`
				}
				_ = json.Unmarshal(rec.Body.Bytes(), &sh)
				alphaShare = sh.ShareID
			}
			target += alphaShare
		}
		var body []byte
		if rt.body != "" {
			body = []byte(rt.body)
		}
		rec := h.do(t, agent.Token, rt.method, target, body, nil)
		if rec.Code >= 300 {
			t.Errorf("granted %s %s = %d (%s), want success", rt.method, target, rec.Code, rec.Body)
		}
	}
}

// TestTokenGrants_ListingsFiltered: GET /collections and GET /crawl only show
// granted collections to a restricted token.
func TestTokenGrants_ListingsFiltered(t *testing.T) {
	h := newHarness(t)
	grantsFixture(t, h)
	agent := mkGrantedToken(t, h, `{"name":"agent","restricted":true,"grants":[{"collection":"alpha","scopes":["collections:read"]}]}`)

	rec := h.do(t, agent.Token, http.MethodGet, "/api/v1/collections", nil, nil)
	var cols listResponse[collectionView]
	_ = json.Unmarshal(rec.Body.Bytes(), &cols)
	if len(cols.Items) != 1 || cols.Items[0].Name != "alpha" {
		t.Errorf("GET /collections = %d %s, want only alpha", rec.Code, rec.Body)
	}

	rec = h.do(t, agent.Token, http.MethodGet, "/api/v1/crawl", nil, nil)
	var crawl struct {
		Collections []crawlCollectionSummary `json:"collections"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &crawl)
	if got := names(crawl.Collections); len(got) != 1 || got[0] != "alpha" {
		t.Errorf("GET /crawl = %v, want [alpha]", got)
	}

	// An unrestricted read token still sees both.
	rec = h.do(t, h.roToken, http.MethodGet, "/api/v1/collections", nil, nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &cols)
	if len(cols.Items) != 2 {
		t.Errorf("unrestricted GET /collections = %s, want both", rec.Body)
	}
}

// TestTokenGrants_ScopePerGrant: each grant carries its own scopes; a granted
// collection without the needed scope is a 403 naming the scope.
func TestTokenGrants_ScopePerGrant(t *testing.T) {
	h := newHarness(t)
	grantsFixture(t, h)
	agent := mkGrantedToken(t, h, `{"name":"agent","restricted":true,"grants":[`+
		`{"collection":"alpha","scopes":["collections:read","collections:write"]},`+
		`{"collection":"beta","scopes":["collections:read"]}]}`)

	mustCode(t, h.do(t, agent.Token, http.MethodGet, "/api/v1/collections/beta/files/f.txt", nil, nil), http.StatusOK)
	mustCode(t, h.do(t, agent.Token, http.MethodPut, "/api/v1/collections/alpha/files/w.txt", []byte("w"), nil), http.StatusCreated)

	rec := h.do(t, agent.Token, http.MethodPut, "/api/v1/collections/beta/files/w.txt", []byte("w"), nil)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "collections:write on beta") {
		t.Errorf("write on read-only grant = %d (%s), want 403 naming the scope", rec.Code, rec.Body)
	}
	// No grant holds delete: rejected at the route gate, before any collection
	// is looked up.
	rec = h.do(t, agent.Token, http.MethodDelete, "/api/v1/collections/alpha/files/w.txt", nil, nil)
	if rec.Code != http.StatusForbidden {
		t.Errorf("delete without any delete grant = %d, want 403", rec.Code)
	}
	// Restricted tokens cannot create collections.
	rec = h.do(t, agent.Token, http.MethodPost, "/api/v1/collections", []byte(`{"name":"gamma"}`), nil)
	if rec.Code != http.StatusForbidden {
		t.Errorf("create collection = %d (%s), want 403", rec.Code, rec.Body)
	}
	all := mkGrantedToken(t, h, `{"name":"all","restricted":true,"grants":[{"collection":"alpha","scopes":`+allGrantScopes+`}]}`)
	rec = h.do(t, all.Token, http.MethodPost, "/api/v1/collections", []byte(`{"name":"gamma"}`), nil)
	if rec.Code != http.StatusForbidden {
		t.Errorf("create collection with all grant scopes = %d (%s), want 403", rec.Code, rec.Body)
	}
	// Admin-only routes stay closed.
	for _, target := range []string{"/api/v1/tokens", "/api/v1/audit", "/api/v1/stats", "/api/v1/upload-requests"} {
		if c := h.do(t, all.Token, http.MethodGet, target, nil, nil).Code; c != http.StatusForbidden {
			t.Errorf("restricted GET %s = %d, want 403", target, c)
		}
	}
}

// TestTokenGrants_NoGrantsReachesNothing: restriction is an explicit flag; a
// restricted token without grants reaches no collection.
func TestTokenGrants_NoGrantsReachesNothing(t *testing.T) {
	h := newHarness(t)
	grantsFixture(t, h)
	agent := mkGrantedToken(t, h, `{"name":"agent","restricted":true}`)
	for _, target := range []string{"/api/v1/collections/alpha", "/api/v1/collections/beta/files/f.txt"} {
		if c := h.do(t, agent.Token, http.MethodGet, target, nil, nil).Code; c != http.StatusForbidden && c != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 403/404", target, c)
		}
	}
	rec := h.do(t, agent.Token, http.MethodGet, "/api/v1/collections", nil, nil)
	if rec.Code == http.StatusOK && !strings.Contains(rec.Body.String(), `"items":[]`) {
		t.Errorf("GET /collections = %s, want nothing listed", rec.Body)
	}
}

// TestTokenGrants_CollectionRecreated: grants bind to the collection's id; a
// re-created collection with the same name is not covered.
func TestTokenGrants_CollectionRecreated(t *testing.T) {
	h := newHarness(t)
	grantsFixture(t, h)
	// The beta grant keeps the token past the route gate once alpha's is gone.
	agent := mkGrantedToken(t, h, `{"name":"agent","restricted":true,"grants":[`+
		`{"collection":"alpha","scopes":["collections:read"]},{"collection":"beta","scopes":["collections:read"]}]}`)
	mustCode(t, h.do(t, agent.Token, http.MethodGet, "/api/v1/collections/alpha", nil, nil), http.StatusOK)

	mustCode(t, h.do(t, h.token, http.MethodDelete, "/api/v1/collections/alpha?force=true", nil, nil), http.StatusNoContent)
	mustCode(t, h.do(t, h.token, http.MethodPost, "/api/v1/collections", []byte(`{"name":"alpha"}`), nil), http.StatusCreated)

	mustCode(t, h.do(t, agent.Token, http.MethodGet, "/api/v1/collections/alpha", nil, nil), http.StatusNotFound)
	rec := h.do(t, h.token, http.MethodGet, "/api/v1/tokens", nil, nil)
	var list listResponse[grantedToken]
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	for _, tok := range list.Items {
		if tok.ID == agent.ID && (!tok.Restricted || len(tok.Grants) != 1 || tok.Grants[0].Collection != "beta") {
			t.Errorf("token after collection delete = %+v, want only the beta grant left", tok)
		}
	}
}

// TestTokenGrants_CreateValidation covers the 400s of design/16 §2 and §5.
func TestTokenGrants_CreateValidation(t *testing.T) {
	h := newHarness(t)
	grantsFixture(t, h)
	for _, body := range []string{
		`{"name":"x","restricted":true,"scopes":["admin"]}`,
		`{"name":"x","restricted":true,"scopes":["collections:read"]}`,
		`{"name":"x","scopes":["collections:read"],"grants":[{"collection":"alpha","scopes":["collections:read"]}]}`,
		`{"name":"x","restricted":true,"grants":[{"collection":"nope","scopes":["collections:read"]}]}`,
		`{"name":"x","restricted":true,"grants":[{"collection":"alpha","scopes":[]}]}`,
		`{"name":"x","restricted":true,"grants":[{"collection":"alpha","scopes":["admin"]}]}`,
		`{"name":"x","restricted":true,"grants":[{"collection":"alpha","scopes":["bogus"]}]}`,
		`{"name":"x","restricted":true,"grants":[{"collection":"alpha","scopes":["collections:read"]},{"collection":"alpha","scopes":["shares:read"]}]}`,
	} {
		if rec := h.do(t, h.token, http.MethodPost, "/api/v1/tokens", []byte(body), nil); rec.Code != http.StatusBadRequest {
			t.Errorf("create %s = %d (%s), want 400", body, rec.Code, rec.Body)
		}
	}
	// An unrestricted token still needs scopes.
	if rec := h.do(t, h.token, http.MethodPost, "/api/v1/tokens", []byte(`{"name":"x"}`), nil); rec.Code != http.StatusBadRequest {
		t.Errorf("unrestricted without scopes = %d, want 400", rec.Code)
	}
}

// TestTokenGrants_ManageGrants: PUT/DELETE /tokens/{id}/grants/{c} are
// admin-only, validated and audited.
func TestTokenGrants_ManageGrants(t *testing.T) {
	h := newHarness(t)
	grantsFixture(t, h)
	agent := mkGrantedToken(t, h, `{"name":"agent","restricted":true,"grants":[{"collection":"alpha","scopes":["collections:read"]}]}`)
	plain := mkGrantedToken(t, h, `{"name":"plain","scopes":["collections:read"]}`)
	grantURL := func(id int64, c string) string {
		return "/api/v1/tokens/" + strconv.FormatInt(id, 10) + "/grants/" + c
	}

	mustCode(t, h.do(t, agent.Token, http.MethodGet, "/api/v1/collections/beta", nil, nil), http.StatusNotFound)
	rec := h.do(t, h.token, http.MethodPut, grantURL(agent.ID, "beta"), []byte(`{"scopes":["collections:read"]}`), nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"beta"`) {
		t.Fatalf("PUT grant = %d (%s)", rec.Code, rec.Body)
	}
	mustCode(t, h.do(t, agent.Token, http.MethodGet, "/api/v1/collections/beta", nil, nil), http.StatusOK)

	// Replacing a grant changes its scopes.
	mustCode(t, h.do(t, agent.Token, http.MethodPut, "/api/v1/collections/beta/files/w.txt", []byte("w"), nil), http.StatusForbidden)
	mustCode(t, h.do(t, h.token, http.MethodPut, grantURL(agent.ID, "beta"), []byte(`{"scopes":["collections:write"]}`), nil), http.StatusOK)
	mustCode(t, h.do(t, agent.Token, http.MethodPut, "/api/v1/collections/beta/files/w.txt", []byte("w"), nil), http.StatusCreated)

	mustCode(t, h.do(t, h.token, http.MethodDelete, grantURL(agent.ID, "beta"), nil, nil), http.StatusNoContent)
	mustCode(t, h.do(t, agent.Token, http.MethodGet, "/api/v1/collections/beta", nil, nil), http.StatusNotFound)
	mustCode(t, h.do(t, h.token, http.MethodDelete, grantURL(agent.ID, "beta"), nil, nil), http.StatusNotFound)

	for _, bad := range []struct {
		target, body string
		want         int
	}{
		{grantURL(plain.ID, "beta"), `{"scopes":["collections:read"]}`, http.StatusBadRequest},
		{grantURL(agent.ID, "nope"), `{"scopes":["collections:read"]}`, http.StatusNotFound},
		{grantURL(agent.ID, "beta"), `{"scopes":[]}`, http.StatusBadRequest},
		{grantURL(agent.ID, "beta"), `{"scopes":["admin"]}`, http.StatusBadRequest},
		{grantURL(99999, "beta"), `{"scopes":["collections:read"]}`, http.StatusNotFound},
		{"/api/v1/tokens/x/grants/beta", `{"scopes":["collections:read"]}`, http.StatusBadRequest},
	} {
		if rec := h.do(t, h.token, http.MethodPut, bad.target, []byte(bad.body), nil); rec.Code != bad.want {
			t.Errorf("PUT %s %s = %d (%s), want %d", bad.target, bad.body, rec.Code, rec.Body, bad.want)
		}
	}
	// Non-admins cannot manage grants, not even their own.
	mustCode(t, h.do(t, agent.Token, http.MethodPut, grantURL(agent.ID, "beta"), []byte(`{"scopes":["collections:read"]}`), nil), http.StatusForbidden)

	entries, err := h.st.QueryAudit(t.Context(), store.AuditFilter{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	var grants, ungrants int
	for _, e := range entries {
		switch e.Action {
		case audit.ActionTokenGrant:
			grants++
		case audit.ActionTokenUngrant:
			ungrants++
		}
	}
	// alpha at creation, beta twice via PUT; one DELETE.
	if grants != 3 || ungrants != 1 {
		t.Errorf("audit token.grant=%d token.ungrant=%d, want 3 and 1", grants, ungrants)
	}
}
