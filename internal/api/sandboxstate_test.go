package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestSandboxStateVisibility: writes, metadata views and the tree show when the
// sandbox is off and when content awaits re-approval (artifactsd-7tl.9).
func TestSandboxStateVisibility(t *testing.T) {
	h := newHarness(t)
	writer := mkToken(t, h.st, "writer", `["collections:write"]`)
	h.do(t, h.token, http.MethodPost, "/api/v1/collections", []byte(`{"name":"demo"}`), nil)
	h.do(t, h.token, http.MethodPatch, "/api/v1/collections/demo/folders/spa?meta", []byte(`{"content":{"sandbox":false}}`), nil)
	h.do(t, h.token, http.MethodPut, "/api/v1/collections/demo/files/spa/admin.html", []byte("a"), nil)
	h.do(t, h.token, http.MethodPut, "/api/v1/collections/demo/files/plain.html", []byte("p"), nil)

	// A user write into the unsandboxed folder answers with a warning.
	rec := h.do(t, writer, http.MethodPut, "/api/v1/collections/demo/files/spa/user.html", []byte("u"), nil)
	var fv fileView
	_ = json.Unmarshal(rec.Body.Bytes(), &fv)
	if rec.Code != http.StatusCreated || len(fv.Warnings) != 1 || !strings.Contains(fv.Warnings[0], "re-approves") {
		t.Errorf("user write into unsandboxed folder: %d %s, want a warning", rec.Code, rec.Body)
	}
	// Writes elsewhere carry none.
	rec = h.do(t, writer, http.MethodPut, "/api/v1/collections/demo/files/other.html", []byte("o"), nil)
	if strings.Contains(rec.Body.String(), "warnings") {
		t.Errorf("plain write has warnings: %s", rec.Body)
	}

	state := func(target string) (string, bool) {
		rec := h.do(t, h.token, http.MethodGet, target, nil, nil)
		var v struct {
			Effective map[string]any `json:"effective"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &v)
		s, _ := v.Effective["sandbox_state"].(string)
		_, note := v.Effective["sandbox_note"]
		return s, note
	}
	for target, want := range map[string]string{
		"/api/v1/collections/demo/files/spa/user.html?meta":  "awaiting_approval",
		"/api/v1/collections/demo/files/spa/admin.html?meta": "unsandboxed",
		"/api/v1/collections/demo/files/plain.html?meta":     "sandboxed",
		"/api/v1/collections/demo/folders/spa?meta":          "unsandboxed",
		"/api/v1/collections/demo/meta":                      "sandboxed",
	} {
		got, note := state(target)
		if got != want || note != (want == "awaiting_approval") {
			t.Errorf("%s: sandbox_state=%q note=%v, want %q", target, got, note, want)
		}
	}

	rec = h.do(t, h.token, http.MethodGet, "/api/v1/collections/demo/tree", nil, nil)
	var page struct {
		Items []treeNode `json:"items"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &page)
	got := map[string]string{}
	for _, n := range page.Items {
		got[n.Path] = n.Sandbox
	}
	for p, want := range map[string]string{"spa": "unsandboxed", "spa/admin.html": "unsandboxed", "spa/user.html": "awaiting_approval", "plain.html": ""} {
		if got[p] != want {
			t.Errorf("tree %s sandbox=%q, want %q", p, got[p], want)
		}
	}
}
