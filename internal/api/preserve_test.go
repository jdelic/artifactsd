package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

const protectedMeta = `{"access":{"visibility":"protected","auth":[{"type":"bearer","token":"s3cret"}]}}`

func ownMeta(t *testing.T, h *harness, p string) string {
	t.Helper()
	rec := h.do(t, h.token, http.MethodGet, "/api/v1/collections/demo/files/"+p+"?meta", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("meta %s = %d (%s)", p, rec.Code, rec.Body)
	}
	var v struct {
		Own json.RawMessage `json:"own"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &v)
	return string(v.Own)
}

func TestPutFile_NoHeaderPreservesMetadata(t *testing.T) {
	h := newHarness(t)
	h.do(t, h.token, http.MethodPost, "/api/v1/collections", []byte(`{"name":"demo"}`), nil)
	hdr := map[string]string{metadataHeader: base64urlNoPad(protectedMeta), "Content-Type": "text/markdown"}
	if rec := h.do(t, h.token, http.MethodPut, "/api/v1/collections/demo/files/secret.md", []byte("v1"), hdr); rec.Code != http.StatusCreated {
		t.Fatalf("put v1 = %d (%s)", rec.Code, rec.Body)
	}

	// Re-upload without the header: content changes, metadata stays.
	rec := h.do(t, h.token, http.MethodPut, "/api/v1/collections/demo/files/secret.md", []byte("v2"), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("put v2 = %d (%s)", rec.Code, rec.Body)
	}
	var fv fileView
	_ = json.Unmarshal(rec.Body.Bytes(), &fv)
	if fv.ContentType != "text/markdown" {
		t.Errorf("content type = %q, want the existing text/markdown kept", fv.ContentType)
	}
	if own := ownMeta(t, h, "secret.md"); !strings.Contains(own, `"protected"`) || !strings.Contains(own, "bearer") {
		t.Errorf("own metadata after re-PUT without header = %s, want protected+bearer kept", own)
	}
	if got := h.do(t, h.token, http.MethodGet, "/api/v1/collections/demo/files/secret.md", nil, nil).Body.String(); got != "v2" {
		t.Errorf("content = %q, want v2", got)
	}

	// With a header the metadata is replaced; e30 ({}) is the explicit reset.
	hdr = map[string]string{metadataHeader: "e30"}
	if rec := h.do(t, h.token, http.MethodPut, "/api/v1/collections/demo/files/secret.md", []byte("v3"), hdr); rec.Code != http.StatusOK {
		t.Fatalf("put v3 = %d", rec.Code)
	}
	if own := ownMeta(t, h, "secret.md"); strings.Contains(own, "protected") {
		t.Errorf("own metadata after explicit {} = %s, want reset", own)
	}
}

func TestLargeUpload_PreservesMetadataUnlessSupplied(t *testing.T) {
	h := newHarness(t)
	h.do(t, h.token, http.MethodPost, "/api/v1/collections", []byte(`{"name":"demo"}`), nil)
	hdr := map[string]string{metadataHeader: base64urlNoPad(protectedMeta)}
	h.do(t, h.token, http.MethodPut, "/api/v1/collections/demo/files/big.bin", []byte("v1"), hdr)

	upload := func(body string) {
		t.Helper()
		rec := h.do(t, h.token, http.MethodPost, "/api/v1/collections/demo/uploads", []byte(body), nil)
		if rec.Code != http.StatusCreated {
			t.Fatalf("begin %s = %d (%s)", body, rec.Code, rec.Body)
		}
		var sess uploadSessionView
		_ = json.Unmarshal(rec.Body.Bytes(), &sess)
		if rec := h.do(t, "", http.MethodPut, "/api/v1/uploads/"+sess.UploadID, []byte("v2"), nil); rec.Code != http.StatusOK {
			t.Fatalf("put = %d (%s)", rec.Code, rec.Body)
		}
	}
	upload(`{"path":"big.bin"}`)
	if own := ownMeta(t, h, "big.bin"); !strings.Contains(own, `"protected"`) {
		t.Errorf("large upload without metadata: own = %s, want protected kept", own)
	}
	upload(`{"path":"big.bin","metadata":{"access":{"visibility":"unlisted"}}}`)
	if own := ownMeta(t, h, "big.bin"); !strings.Contains(own, `"unlisted"`) || strings.Contains(own, "protected") {
		t.Errorf("large upload with metadata: own = %s, want replaced", own)
	}
}
