package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const sandboxOff = `{"content":{"sandbox":false}}`

// TestSandboxOff_RequiresAdmin: only admin-scoped tokens may disable the
// sandbox, on every path that accepts a metadata document (artifactsd-m9e.4).
func TestSandboxOff_RequiresAdmin(t *testing.T) {
	h := newHarness(t)
	h.do(t, h.token, http.MethodPost, "/api/v1/collections", []byte(`{"name":"demo"}`), nil)
	h.do(t, h.token, http.MethodPut, "/api/v1/collections/demo/files/d/x.html", []byte("<p>x</p>"), nil)
	rec := h.do(t, h.token, http.MethodPost, "/api/v1/collections/demo/shares", []byte(`{"target_path":""}`), nil)
	var sh struct {
		ShareID string `json:"share_id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &sh)

	writer := mkToken(t, h.st, "writer", `["collections:write"]`)
	sharer := mkToken(t, h.st, "sharer", `["shares:write"]`)
	hdr := map[string]string{metadataHeader: base64urlNoPad(sandboxOff)}

	cases := []struct {
		name, token, method, target string
		body                        string
		headers                     map[string]string
	}{
		{"put file", writer, http.MethodPut, "/api/v1/collections/demo/files/y.html", "<p>y</p>", hdr},
		{"patch file meta", writer, http.MethodPatch, "/api/v1/collections/demo/files/d/x.html?meta", sandboxOff, nil},
		{"patch folder meta", writer, http.MethodPatch, "/api/v1/collections/demo/folders/d?meta", sandboxOff, nil},
		{"patch collection meta", writer, http.MethodPatch, "/api/v1/collections/demo/meta", sandboxOff, nil},
		{"create collection", writer, http.MethodPost, "/api/v1/collections", `{"name":"other","metadata":` + sandboxOff + `}`, nil},
		{"begin large upload", writer, http.MethodPost, "/api/v1/collections/demo/uploads", `{"path":"z.html","metadata":` + sandboxOff + `}`, nil},
		{"create share", sharer, http.MethodPost, "/api/v1/collections/demo/shares", `{"target_path":"","metadata":` + sandboxOff + `}`, nil},
		{"patch share", sharer, http.MethodPatch, "/api/v1/shares/" + sh.ShareID, sandboxOff, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := h.do(t, c.token, c.method, c.target, []byte(c.body), c.headers)
			if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "requires an admin-scoped token") {
				t.Fatalf("%s %s = %d (%s), want 403", c.method, c.target, rec.Code, rec.Body)
			}
			// The admin token may do the same thing.
			body := strings.ReplaceAll(c.body, `"other"`, `"other2"`)
			body = strings.ReplaceAll(body, "z.html", "z2.html")
			if rec := h.do(t, h.token, c.method, c.target, []byte(body), c.headers); rec.Code >= 300 {
				t.Errorf("admin: %s %s = %d (%s)", c.method, c.target, rec.Code, rec.Body)
			}
		})
	}
	if c := h.do(t, writer, http.MethodGet, "/api/v1/collections/demo/files/y.html", nil, nil).Code; c != http.StatusOK {
		t.Errorf("admin-written y.html: GET = %d", c)
	}
}

func TestSandboxOff_NonAdminCanTouchOtherFields(t *testing.T) {
	h := newHarness(t)
	h.do(t, h.token, http.MethodPost, "/api/v1/collections", []byte(`{"name":"demo"}`), nil)
	hdr := map[string]string{metadataHeader: base64urlNoPad(sandboxOff)}
	if rec := h.do(t, h.token, http.MethodPut, "/api/v1/collections/demo/files/app.html", []byte("<p/>"), hdr); rec.Code != http.StatusCreated {
		t.Fatalf("admin put = %d (%s)", rec.Code, rec.Body)
	}
	writer := mkToken(t, h.st, "writer", `["collections:write"]`)
	// An unrelated patch keeps the admin-set sandbox=false.
	if rec := h.do(t, writer, http.MethodPatch, "/api/v1/collections/demo/files/app.html?meta",
		[]byte(`{"crawl":{"indexable":false}}`), nil); rec.Code != http.StatusOK {
		t.Fatalf("unrelated patch = %d (%s)", rec.Code, rec.Body)
	}
	if own := ownMeta(t, h, "app.html"); !strings.Contains(own, `"sandbox":false`) {
		t.Errorf("own = %s, want sandbox false kept", own)
	}
	// Setting sandbox true (or leaving it unset) is always allowed.
	if rec := h.do(t, writer, http.MethodPatch, "/api/v1/collections/demo/files/app.html?meta",
		[]byte(`{"content":{"sandbox":true}}`), nil); rec.Code != http.StatusOK {
		t.Errorf("sandbox true by writer = %d (%s)", rec.Code, rec.Body)
	}
}

// TestAuthEntryValidation: argon2 costs, token_hash shape and the entry cap are
// enforced on write (artifactsd-m9e.9).
func TestAuthEntryValidation(t *testing.T) {
	h := newHarness(t)
	h.do(t, h.token, http.MethodPost, "/api/v1/collections", []byte(`{"name":"demo"}`), nil)
	h.do(t, h.token, http.MethodPut, "/api/v1/collections/demo/files/f.txt", []byte("x"), nil)
	patch := func(auth string) *httptest.ResponseRecorder {
		return h.do(t, h.token, http.MethodPatch, "/api/v1/collections/demo/files/f.txt?meta",
			[]byte(`{"access":{"visibility":"protected","auth":`+auth+`}}`), nil)
	}
	slow := `[{"type":"basic","username":"u","password_hash":"$argon2id$v=19$m=262144,t=40,p=1$c2FsdHNhbHQ$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}]`
	if rec := patch(slow); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "out of bounds") {
		t.Errorf("expensive password_hash = %d (%s), want 400", rec.Code, rec.Body)
	}
	if rec := patch(`[{"type":"bearer","token_hash":"ABC"}]`); rec.Code != http.StatusBadRequest {
		t.Errorf("malformed token_hash = %d, want 400", rec.Code)
	}
	many := "[" + strings.TrimSuffix(strings.Repeat(`{"type":"bearer","token":"t"},`, 17), ",") + "]"
	if rec := patch(many); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "max 16") {
		t.Errorf("17 auth entries = %d (%s), want 400", rec.Code, rec.Body)
	}
	if rec := patch(`[{"type":"basic","username":"u","password":"pw"}]`); rec.Code != http.StatusOK {
		t.Errorf("cleartext password (server-hashed) = %d (%s), want 200", rec.Code, rec.Body)
	}
}

func TestAuthEntryValidation_DuplicateBasicUsernames(t *testing.T) {
	h := newHarness(t)
	h.do(t, h.token, http.MethodPost, "/api/v1/collections", []byte(`{"name":"demo"}`), nil)
	h.do(t, h.token, http.MethodPut, "/api/v1/collections/demo/files/f.txt", []byte("x"), nil)
	rec := h.do(t, h.token, http.MethodPatch, "/api/v1/collections/demo/files/f.txt?meta",
		[]byte(`{"access":{"visibility":"protected","auth":[{"type":"basic","username":"u","password":"a"},{"type":"basic","username":"u","password":"b"}]}}`), nil)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "duplicate") {
		t.Errorf("duplicate basic usernames = %d (%s), want 400", rec.Code, rec.Body)
	}
}
