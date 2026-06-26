package janitor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jdelic/artifactsd/internal/audit"
	"github.com/jdelic/artifactsd/internal/blob"
	"github.com/jdelic/artifactsd/internal/store"
)

func newTestJanitor(t *testing.T) (*Janitor, *store.Store, *blob.FSStore, context.Context) {
	j, st, bs, ctx, _ := newTestJanitorDir(t)
	return j, st, bs, ctx
}

func newTestJanitorDir(t *testing.T) (*Janitor, *store.Store, *blob.FSStore, context.Context, string) {
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
	return j, st, bs, ctx, dir
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

func TestJanitor_ExpiresUploadSession(t *testing.T) {
	j, st, _, ctx := newTestJanitor(t)
	c := mustCollection(t, st, ctx, "demo", nil)

	pending := &store.UploadSession{
		UploadID: "upl_pending_expired", CollectionID: c.ID, TargetPath: "a.bin",
		Status: store.UploadPending, ExpiresAt: *past(t),
	}
	if err := st.CreateUploadSession(ctx, pending); err != nil {
		t.Fatalf("CreateUploadSession: %v", err)
	}
	fresh := &store.UploadSession{
		UploadID: "upl_pending_fresh", CollectionID: c.ID, TargetPath: "b.bin",
		Status: store.UploadPending, ExpiresAt: *future(t),
	}
	if err := st.CreateUploadSession(ctx, fresh); err != nil {
		t.Fatalf("CreateUploadSession: %v", err)
	}

	result, err := j.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if got := result.(*Summary).UploadSessionsExpired; got != 1 {
		t.Errorf("UploadSessionsExpired = %d, want 1", got)
	}

	got, err := st.GetUploadSession(ctx, pending.UploadID)
	if err != nil || got.Status != store.UploadExpired {
		t.Errorf("expired session = %+v, %v", got, err)
	}
	got, err = st.GetUploadSession(ctx, fresh.UploadID)
	if err != nil || got.Status != store.UploadPending {
		t.Errorf("fresh session was touched: %+v, %v", got, err)
	}
}

func TestJanitor_Reconcile(t *testing.T) {
	j, st, bs, ctx, dir := newTestJanitorDir(t)
	c := mustCollection(t, st, ctx, "demo", nil)

	// A node references this blob — Reconcile must leave it alone.
	referenced := mustFileNode(t, st, bs, ctx, c.ID, "kept.bin", "kept content", nil)

	// An orphan blob with no referencing node at all (simulates a crash
	// between blob.Put committing and the node write, design/07 §6).
	orphanRef, _, _, err := bs.Put(ctx, strings.NewReader("orphan content"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	// A stale temp file, as left behind by an interrupted upload. j's
	// uploadTTL defaults to defaultUploadTTL (1h); back-date well past that.
	stale := filepath.Join(dir, "tmp", "blob-stale")
	if err := os.WriteFile(stale, []byte("partial"), 0o600); err != nil {
		t.Fatalf("write stale temp: %v", err)
	}
	staleTime := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(stale, staleTime, staleTime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	sum, err := j.Reconcile(ctx)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if sum.OrphanBlobsDeleted != 1 {
		t.Errorf("OrphanBlobsDeleted = %d, want 1", sum.OrphanBlobsDeleted)
	}
	if sum.TempFilesRemoved != 1 {
		t.Errorf("TempFilesRemoved = %d, want 1", sum.TempFilesRemoved)
	}
	if exists, _ := bs.Exists(ctx, orphanRef); exists {
		t.Error("orphan blob still exists after Reconcile")
	}
	if exists, _ := bs.Exists(ctx, blob.Ref(*referenced.BlobID)); !exists {
		t.Error("referenced blob was deleted by Reconcile")
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("stale temp file was not removed")
	}
}

func TestJanitor_Scrub_Clean(t *testing.T) {
	j, st, bs, ctx := newTestJanitor(t)
	c := mustCollection(t, st, ctx, "demo", nil)
	mustFileNode(t, st, bs, ctx, c.ID, "a.bin", "clean content", nil)
	mustFileNode(t, st, bs, ctx, c.ID, "b.bin", "other content", nil)

	sum, err := j.Scrub(ctx)
	if err != nil {
		t.Fatalf("Scrub: %v", err)
	}
	if sum.BlobsVerified != 2 {
		t.Errorf("BlobsVerified = %d, want 2", sum.BlobsVerified)
	}
	if sum.BlobsCorrupted != 0 || len(sum.Errors) != 0 {
		t.Errorf("clean store reported corruption: %+v", sum)
	}
}

func TestJanitor_Scrub_Bitrot(t *testing.T) {
	j, st, bs, ctx, dir := newTestJanitorDir(t)
	c := mustCollection(t, st, ctx, "demo", nil)

	// Store one clean blob and one that we'll corrupt.
	mustFileNode(t, st, bs, ctx, c.ID, "clean.bin", "untouched content", nil)
	n := mustFileNode(t, st, bs, ctx, c.ID, "corrupt.bin", "content to corrupt", nil)

	// Corrupt the blob by overwriting it on disk (bitrot simulation).
	blobID := *n.BlobID
	blobRef := blob.Ref(blobID)
	sum, err := blobRef.Sum()
	if err != nil {
		t.Fatalf("ref.Sum: %v", err)
	}
	corruptPath := filepath.Join(dir, "blobs", "sha256", sum[0:2], sum[2:4], sum)
	if err := os.WriteFile(corruptPath, []byte("corrupted bytes"), 0o600); err != nil {
		t.Fatalf("corrupt blob file: %v", err)
	}

	result, err := j.Scrub(ctx)
	if err != nil {
		t.Fatalf("Scrub: %v", err)
	}
	if result.BlobsCorrupted != 1 {
		t.Errorf("BlobsCorrupted = %d, want 1", result.BlobsCorrupted)
	}
	if result.BlobsVerified != 1 {
		t.Errorf("BlobsVerified = %d, want 1 (the clean blob)", result.BlobsVerified)
	}
	if len(result.Errors) != 1 {
		t.Errorf("Errors = %v, want exactly one corruption error", result.Errors)
	}
}

func TestJanitor_LoopStopsOnContextCancel(t *testing.T) {
	j, _, _, _ := newTestJanitor(t)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		j.Loop(ctx, 5*time.Millisecond)
		close(done)
	}()

	time.Sleep(20 * time.Millisecond) // let it tick a few times
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Loop did not return after context cancellation")
	}
}
