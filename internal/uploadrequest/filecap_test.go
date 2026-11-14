package uploadrequest_test

import (
	"net/http"
	"testing"

	"github.com/jdelic/artifactsd/internal/testserver"
)

func TestUpload_CollectionFileCap(t *testing.T) {
	ts := testserver.NewWithEnv(t, map[string]string{"ARTIFACTSD_MAX_FILES_PER_COLLECTION": "2"})
	createCollection(t, ts, "demo")
	id := createLink(t, ts, `{"collection":"demo","target_folder":"in"}`)
	if code, b := upload(t, ts, id, part{"a.txt", "a"}); code != http.StatusOK {
		t.Fatalf("first upload: %d %s", code, b)
	}
	// Two new files would make three: the whole request is rejected.
	if code, b := upload(t, ts, id, part{"b.txt", "b"}, part{"c.txt", "c"}); code != http.StatusConflict {
		t.Fatalf("upload over the collection cap: %d %s, want 409", code, b)
	}
	assertAbsent(t, ts, "demo", "in/b.txt", "in/c.txt")
	if code, b := upload(t, ts, id, part{"b.txt", "b"}); code != http.StatusOK {
		t.Fatalf("upload filling the cap: %d %s", code, b)
	}
}
