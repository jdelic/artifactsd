package serve_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/jdelic/artifactsd/internal/testserver"
)

// TestOIDCIssuerAllowlist: metadata may only name allowlisted issuers, and an
// already-stored entry naming any other issuer never triggers outbound
// requests and never serves the object (artifactsd-m9e.21).
func TestOIDCIssuerAllowlist(t *testing.T) {
	var hits atomic.Int64
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(idp.Close)
	const allowed = "http://127.0.0.1:1" // allowlisted but unreachable

	ts := testserver.NewWithEnv(t, map[string]string{"ARTIFACTSD_OIDC_ALLOWED_ISSUERS": allowed})
	jsonHdr := map[string]string{"Content-Type": "application/json"}
	call(t, ts, http.MethodPost, "/api/v1/collections", ts.AdminToken, `{"name":"demo"}`, jsonHdr)
	call(t, ts, http.MethodPut, "/api/v1/collections/demo/files/f.txt", ts.AdminToken, "secret", nil)

	patch := func(issuer string) int {
		return call(t, ts, http.MethodPatch, "/api/v1/collections/demo/files/f.txt?meta", ts.AdminToken,
			`{"access":{"visibility":"protected","auth":[{"type":"oidc","issuer":"`+issuer+`","audience":"a","any_authenticated":true}]}}`,
			jsonHdr).StatusCode
	}
	if c := patch(idp.URL); c != http.StatusBadRequest {
		t.Errorf("PATCH with unlisted issuer = %d, want 400", c)
	}
	if c := patch(allowed + "/"); c != http.StatusOK {
		t.Errorf("PATCH with allowlisted issuer = %d, want 200", c)
	}

	// Store an unlisted issuer directly, as if written before the allowlist.
	ctx := context.Background()
	c, err := ts.Store.GetCollectionByName(ctx, "demo")
	if err != nil {
		t.Fatal(err)
	}
	n, err := ts.Store.GetNode(ctx, c.ID, "f.txt")
	if err != nil {
		t.Fatal(err)
	}
	n.Metadata = []byte(`{"access":{"visibility":"protected","auth":[{"type":"oidc","issuer":"` + idp.URL +
		`","audience":"a","any_authenticated":true}]}}`)
	if err := ts.Store.UpsertNode(ctx, n); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		r := call(t, ts, http.MethodGet, "/c/demo/f.txt", "", "", map[string]string{"Authorization": "Bearer x.y.z"})
		if r.StatusCode == http.StatusOK {
			t.Fatal("object with an unlisted OIDC issuer was served")
		}
		if r.StatusCode != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", r.StatusCode)
		}
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("unlisted issuer received %d requests, want 0", n)
	}
}
