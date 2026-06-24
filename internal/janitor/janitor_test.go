package janitor

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jdelic/artifactsd/internal/audit"
	"github.com/jdelic/artifactsd/internal/blob"
	"github.com/jdelic/artifactsd/internal/store"
)

func newTestJanitor(t *testing.T) (*Janitor, *store.Store, blob.Store, context.Context) {
	t.Helper()
	dir := t.TempDir()
	ctx := context.Background()

	db, err := store.Open(ctx, filepath.Join(dir, store.DBFilename))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	st := store.New(db)

	bs, err := blob.NewFSStore(dir)
	if err != nil {
		t.Fatalf("blob: %v", err)
	}

	j := New(Deps{Store: st, Blob: bs, Audit: audit.NewRecorder(st.AuditSink(), nil)})
	return j, st, bs, ctx
}

func mustCollection(t *testing.T, st *store.Store, ctx context.Context, name string, expiresAt *time.Time) *store.Collection {
	t.Helper()
	c := &store.Collection{Name: name, Visibility: "public", ExpiresAt: expiresAt}
	if err := st.CreateCollection(ctx, c); err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}
	return c
}

func mustFileNode(t *testing.T, st *store.Store, bs blob.Store, ctx context.Context, collectionID int64, path, content string, expiresAt *time.Time) *store.Node {
	t.Helper()
	ref, size, sum, err := bs.Put(ctx, strings.NewReader(content))
	if err != nil {
		t.Fatalf("blob.Put: %v", err)
	}
	refStr := string(ref)
	n := &store.Node{
		CollectionID: collectionID, Path: path, Name: path, Kind: store.KindFile,
		BlobID: &refStr, Size: &size, Checksum: &sum, Metadata: []byte(`{}`), ExpiresAt: expiresAt,
	}
	if err := st.UpsertNode(ctx, n); err != nil {
		t.Fatalf("UpsertNode: %v", err)
	}
	return n
}

func past(t *testing.T) *time.Time {
	t.Helper()
	when := time.Now().UTC().Add(-time.Hour)
	return &when
}

func future(t *testing.T) *time.Time {
	t.Helper()
	when := time.Now().UTC().Add(time.Hour)
	return &when
}

func TestJanitor_ExpiresFile(t *testing.T) {
	j, st, bs, ctx := newTestJanitor(t)
	c := mustCollection(t, st, ctx, "demo", nil)
	n := mustFileNode(t, st, bs, ctx, c.ID, "a.txt", "hello", past(t))

	result, err := j.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	sum := result.(*Summary)
	if sum.NodesDeleted != 1 || sum.BlobsDeleted != 1 {
		t.Errorf("summary = %+v", sum)
	}

	if _, err := st.GetNodeByID(ctx, n.ID); err == nil {
		t.Error("node still exists after expiry")
	}
	if exists, _ := bs.Exists(ctx, blob.Ref(*n.BlobID)); exists {
		t.Error("blob still exists after its only referencing node was deleted")
	}

	logs, _ := st.ListAudit(ctx, 10)
	if len(logs) != 1 || logs[0].Action != audit.ActionExpiryDelete || logs[0].Actor == nil || *logs[0].Actor != audit.ActorJanitor {
		t.Errorf("audit logs = %+v", logs)
	}
}

func TestJanitor_DedupedBlobSurvivesUntilLastRef(t *testing.T) {
	j, st, bs, ctx := newTestJanitor(t)
	c := mustCollection(t, st, ctx, "demo", nil)
	n1 := mustFileNode(t, st, bs, ctx, c.ID, "a.txt", "same bytes", past(t))
	n2 := mustFileNode(t, st, bs, ctx, c.ID, "b.txt", "same bytes", nil) // not expired, same content
	if *n1.BlobID != *n2.BlobID {
		t.Fatalf("expected dedup, got distinct refs %q vs %q", *n1.BlobID, *n2.BlobID)
	}

	if _, err := j.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if _, err := st.GetNodeByID(ctx, n1.ID); err == nil {
		t.Error("expired node still exists")
	}
	if exists, _ := bs.Exists(ctx, blob.Ref(*n1.BlobID)); !exists {
		t.Error("blob removed while still referenced by b.txt")
	}

	// Now expire the second reference too.
	if err := st.RecomputeNodeDenormalized(ctx, n2.ID); err != nil {
		t.Fatalf("recompute: %v", err)
	}
	exp := past(t)
	n2.ExpiresAt = exp
	if err := st.UpsertNode(ctx, n2); err != nil {
		t.Fatalf("UpsertNode: %v", err)
	}
	if _, err := j.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if exists, _ := bs.Exists(ctx, blob.Ref(*n1.BlobID)); exists {
		t.Error("blob still exists after its last referencing node was deleted")
	}
}

func TestJanitor_ExpiresCollectionCascade(t *testing.T) {
	j, st, bs, ctx := newTestJanitor(t)
	c := mustCollection(t, st, ctx, "demo", past(t))
	n := mustFileNode(t, st, bs, ctx, c.ID, "a.txt", "hello", nil) // node itself not expired

	result, err := j.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	sum := result.(*Summary)
	if sum.CollectionsDeleted != 1 || sum.BlobsDeleted != 1 {
		t.Errorf("summary = %+v", sum)
	}
	if _, err := st.GetCollection(ctx, c.ID); err == nil {
		t.Error("collection still exists after expiry")
	}
	if _, err := st.GetNodeByID(ctx, n.ID); err == nil {
		t.Error("node survived its collection's cascade delete")
	}
}

func TestJanitor_ExpiresShare(t *testing.T) {
	j, st, _, ctx := newTestJanitor(t)
	c := mustCollection(t, st, ctx, "demo", nil)
	sh := &store.Share{
		ShareID: "shr_test", CollectionID: c.ID, TargetKind: "collection",
		Metadata: []byte(`{"expiry":{"expires_at":"2020-01-01T00:00:00Z"}}`),
	}
	if err := st.CreateShare(ctx, sh); err != nil {
		t.Fatalf("CreateShare: %v", err)
	}

	result, err := j.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.(*Summary).SharesDeleted != 1 {
		t.Errorf("summary = %+v", result)
	}
	if _, err := st.GetShare(ctx, sh.ShareID); err == nil {
		t.Error("share still exists after expiry")
	}
}

func TestJanitor_UnexpiredUntouched(t *testing.T) {
	j, st, bs, ctx := newTestJanitor(t)
	c := mustCollection(t, st, ctx, "demo", future(t))
	n := mustFileNode(t, st, bs, ctx, c.ID, "a.txt", "hello", future(t))

	result, err := j.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	sum := result.(*Summary)
	if sum.NodesDeleted != 0 || sum.CollectionsDeleted != 0 || sum.SharesDeleted != 0 {
		t.Errorf("summary = %+v, want all zero", sum)
	}
	if _, err := st.GetCollection(ctx, c.ID); err != nil {
		t.Error("unexpired collection was deleted")
	}
	if _, err := st.GetNodeByID(ctx, n.ID); err != nil {
		t.Error("unexpired node was deleted")
	}
}
