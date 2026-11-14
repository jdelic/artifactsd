package serve

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/jdelic/artifactsd/internal/store"
)

// TestResolveChain_FailsClosedOnLookupError: a transient DB error on a private
// ancestor must never let the object be served (artifactsd-m9e.14).
func TestResolveChain_FailsClosedOnLookupError(t *testing.T) {
	h := newHarness(t)
	c := h.collection(t, "demo", `{"access":{"visibility":"public","directory_listing":true}}`)
	h.file(t, c, "secret/f.txt", "classified", `{}`, "text/plain")
	h.folderMeta(t, c, "secret", `{"access":{"visibility":"private"}}`)
	if rec := h.get("/c/demo/secret/f.txt", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("baseline: private ancestor = %d, want 404", rec.Code)
	}

	orig := h.h.getNode
	h.h.getNode = func(ctx context.Context, collID int64, p string) (*store.Node, error) {
		if p == "secret" {
			return nil, errors.New("database is locked")
		}
		return orig(ctx, collID, p)
	}
	for _, target := range []string{"/c/demo/secret/f.txt", "/c/demo/secret/"} {
		if rec := h.get(target, nil); rec.Code != http.StatusInternalServerError {
			t.Errorf("%s with failing ancestor lookup = %d (%q), want 500", target, rec.Code, rec.Body.String())
		}
	}
}

// TestResolveChain_FailsClosedOnCorruptMetadata: an unparseable stored
// document on a protecting ancestor is an error, not an empty layer.
func TestResolveChain_FailsClosedOnCorruptMetadata(t *testing.T) {
	h := newHarness(t)
	c := h.collection(t, "demo", `{"access":{"visibility":"public"}}`)
	h.file(t, c, "secret/f.txt", "classified", `{}`, "text/plain")
	h.folderMeta(t, c, "secret", `{"access":{"visibility":"private"}}`)
	if _, err := h.db.SQL().ExecContext(context.Background(),
		`UPDATE nodes SET metadata = '{"access": {broken', visibility = NULL WHERE path = 'secret'`); err != nil {
		t.Fatal(err)
	}
	if rec := h.get("/c/demo/secret/f.txt", nil); rec.Code != http.StatusInternalServerError {
		t.Errorf("corrupt ancestor metadata = %d (%q), want 500", rec.Code, rec.Body.String())
	}
}
