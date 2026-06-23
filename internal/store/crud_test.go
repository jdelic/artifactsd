package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

var fixedNow = time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC)

// newStore opens a migrated temp DB wrapped in a Store with a fixed clock.
func newStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	db := openTestDB(t)
	ctx := context.Background()
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	s := New(db).WithClock(func() time.Time { return fixedNow })
	return s, ctx
}

func mustCollection(t *testing.T, s *Store, ctx context.Context, name string) *Collection {
	t.Helper()
	c := &Collection{Name: name, Visibility: "public"}
	if err := s.CreateCollection(ctx, c); err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}
	return c
}

func TestCollections_CRUD(t *testing.T) {
	s, ctx := newStore(t)

	c := mustCollection(t, s, ctx, "demo")
	if c.ID == 0 {
		t.Fatal("collection ID not set")
	}
	if !c.CreatedAt.Equal(fixedNow) {
		t.Errorf("CreatedAt = %v, want %v", c.CreatedAt, fixedNow)
	}

	got, err := s.GetCollectionByName(ctx, "demo")
	if err != nil || got.ID != c.ID {
		t.Fatalf("GetCollectionByName: %v (%+v)", err, got)
	}
	byID, err := s.GetCollection(ctx, c.ID)
	if err != nil || byID.Name != "demo" {
		t.Fatalf("GetCollection: %v", err)
	}

	exp := fixedNow.Add(24 * time.Hour)
	c.Metadata = []byte(`{"visibility":"private"}`)
	c.Visibility = "private"
	c.ExpiresAt = &exp
	if err := s.UpdateCollectionMetadata(ctx, c); err != nil {
		t.Fatalf("UpdateCollectionMetadata: %v", err)
	}
	reloaded, _ := s.GetCollection(ctx, c.ID)
	if reloaded.Visibility != "private" || reloaded.ExpiresAt == nil || !reloaded.ExpiresAt.Equal(exp) {
		t.Errorf("update not persisted: %+v", reloaded)
	}

	list, _ := s.ListCollections(ctx)
	if len(list) != 1 {
		t.Errorf("ListCollections len = %d, want 1", len(list))
	}

	if err := s.DeleteCollection(ctx, c.ID); err != nil {
		t.Fatalf("DeleteCollection: %v", err)
	}
	if _, err := s.GetCollection(ctx, c.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestCollections_NotFound(t *testing.T) {
	s, ctx := newStore(t)
	if _, err := s.GetCollectionByName(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("got %v, want ErrNotFound", err)
	}
	if err := s.DeleteCollection(ctx, 999); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete missing: got %v, want ErrNotFound", err)
	}
}

func TestNodes_UpsertInsertThenUpdate(t *testing.T) {
	s, ctx := newStore(t)
	c := mustCollection(t, s, ctx, "demo")

	blob := "sha256:abc"
	var size int64 = 12
	n := &Node{
		CollectionID: c.ID, Path: "a/b.txt", Name: "b.txt", Kind: KindFile,
		BlobID: &blob, Size: &size, Metadata: []byte(`{}`),
	}
	if err := s.UpsertNode(ctx, n); err != nil {
		t.Fatalf("UpsertNode insert: %v", err)
	}
	firstID := n.ID
	if firstID == 0 {
		t.Fatal("node ID not set on insert")
	}

	// Update same path: ID stable, created_at preserved.
	newSize := int64(99)
	n2 := &Node{
		CollectionID: c.ID, Path: "a/b.txt", Name: "b.txt", Kind: KindFile,
		BlobID: &blob, Size: &newSize, Metadata: []byte(`{"x":1}`),
	}
	if err := s.UpsertNode(ctx, n2); err != nil {
		t.Fatalf("UpsertNode update: %v", err)
	}
	if n2.ID != firstID {
		t.Errorf("upsert changed ID: %d -> %d", firstID, n2.ID)
	}

	got, _ := s.GetNode(ctx, c.ID, "a/b.txt")
	if got.Size == nil || *got.Size != 99 {
		t.Errorf("size not updated: %+v", got.Size)
	}
	if !got.CreatedAt.Equal(fixedNow) {
		t.Errorf("created_at not preserved: %v", got.CreatedAt)
	}
}

func TestNodes_TreeAndCascade(t *testing.T) {
	s, ctx := newStore(t)
	c := mustCollection(t, s, ctx, "demo")

	folder := &Node{CollectionID: c.ID, Path: "dir", Name: "dir", Kind: KindFolder, Metadata: []byte(`{}`)}
	if err := s.UpsertNode(ctx, folder); err != nil {
		t.Fatalf("upsert folder: %v", err)
	}
	child := &Node{
		CollectionID: c.ID, ParentID: &folder.ID, Path: "dir/f.txt", Name: "f.txt",
		Kind: KindFile, Metadata: []byte(`{}`),
	}
	if err := s.UpsertNode(ctx, child); err != nil {
		t.Fatalf("upsert child: %v", err)
	}

	roots, _ := s.ListChildren(ctx, c.ID, nil)
	if len(roots) != 1 || roots[0].Path != "dir" {
		t.Errorf("root children = %+v, want [dir]", roots)
	}
	kids, _ := s.ListChildren(ctx, c.ID, &folder.ID)
	if len(kids) != 1 || kids[0].Path != "dir/f.txt" {
		t.Errorf("dir children = %+v, want [dir/f.txt]", kids)
	}

	// Deleting the folder cascades to the child via parent_id.
	if err := s.DeleteNode(ctx, folder.ID); err != nil {
		t.Fatalf("DeleteNode: %v", err)
	}
	all, _ := s.ListCollectionNodes(ctx, c.ID)
	if len(all) != 0 {
		t.Errorf("nodes after cascade = %d, want 0", len(all))
	}
}

func TestNodes_TouchLastAccess(t *testing.T) {
	s, ctx := newStore(t)
	c := mustCollection(t, s, ctx, "demo")
	n := &Node{CollectionID: c.ID, Path: "f", Name: "f", Kind: KindFile, Metadata: []byte(`{}`)}
	_ = s.UpsertNode(ctx, n)

	at := fixedNow.Add(time.Hour)
	if err := s.TouchLastAccess(ctx, n.ID, at); err != nil {
		t.Fatalf("TouchLastAccess: %v", err)
	}
	got, _ := s.GetNodeByID(ctx, n.ID)
	if got.LastAccessAt == nil || !got.LastAccessAt.Equal(at) {
		t.Errorf("last_access_at = %v, want %v", got.LastAccessAt, at)
	}
}

func TestShares_CRUD(t *testing.T) {
	s, ctx := newStore(t)
	c := mustCollection(t, s, ctx, "demo")

	sh := &Share{ShareID: "7V9qWb3KxLm2", CollectionID: c.ID, TargetKind: "collection"}
	if err := s.CreateShare(ctx, sh); err != nil {
		t.Fatalf("CreateShare: %v", err)
	}
	got, err := s.GetShare(ctx, "7V9qWb3KxLm2")
	if err != nil || got.CollectionID != c.ID {
		t.Fatalf("GetShare: %v (%+v)", err, got)
	}

	if err := s.RevokeShare(ctx, sh.ShareID, fixedNow); err != nil {
		t.Fatalf("RevokeShare: %v", err)
	}
	got, _ = s.GetShare(ctx, sh.ShareID)
	if got.RevokedAt == nil {
		t.Error("share not revoked")
	}
	// Second revoke is a no-op (already revoked) → ErrNotFound by the guard.
	if err := s.RevokeShare(ctx, sh.ShareID, fixedNow); !errors.Is(err, ErrNotFound) {
		t.Errorf("double revoke: got %v, want ErrNotFound", err)
	}

	list, _ := s.ListSharesByCollection(ctx, c.ID)
	if len(list) != 1 {
		t.Errorf("ListShares len = %d, want 1", len(list))
	}
}

func TestShares_CascadeOnCollectionDelete(t *testing.T) {
	s, ctx := newStore(t)
	c := mustCollection(t, s, ctx, "demo")
	_ = s.CreateShare(ctx, &Share{ShareID: "abc123def456", CollectionID: c.ID, TargetKind: "collection"})

	if err := s.DeleteCollection(ctx, c.ID); err != nil {
		t.Fatalf("DeleteCollection: %v", err)
	}
	if _, err := s.GetShare(ctx, "abc123def456"); !errors.Is(err, ErrNotFound) {
		t.Errorf("share survived collection delete: %v", err)
	}
}

func TestUploadSessions_Lifecycle(t *testing.T) {
	s, ctx := newStore(t)
	c := mustCollection(t, s, ctx, "demo")

	u := &UploadSession{
		UploadID: "upl_8f31c2a9", CollectionID: c.ID, TargetPath: "big.bin",
		ExpiresAt: fixedNow.Add(time.Hour),
	}
	if err := s.CreateUploadSession(ctx, u); err != nil {
		t.Fatalf("CreateUploadSession: %v", err)
	}
	if u.Status != UploadPending {
		t.Errorf("status = %q, want pending", u.Status)
	}

	if err := s.CompleteUploadSession(ctx, u.UploadID, "sha256:xyz", fixedNow); err != nil {
		t.Fatalf("CompleteUploadSession: %v", err)
	}
	got, _ := s.GetUploadSession(ctx, u.UploadID)
	if got.Status != UploadCompleted || got.TempBlobID == nil {
		t.Errorf("not completed: %+v", got)
	}
}

func TestUploadSessions_ListExpired(t *testing.T) {
	s, ctx := newStore(t)
	c := mustCollection(t, s, ctx, "demo")

	_ = s.CreateUploadSession(ctx, &UploadSession{
		UploadID: "upl_old", CollectionID: c.ID, TargetPath: "a", ExpiresAt: fixedNow.Add(-time.Minute),
	})
	_ = s.CreateUploadSession(ctx, &UploadSession{
		UploadID: "upl_new", CollectionID: c.ID, TargetPath: "b", ExpiresAt: fixedNow.Add(time.Hour),
	})

	expired, err := s.ListExpiredUploadSessions(ctx, fixedNow, 10)
	if err != nil {
		t.Fatalf("ListExpiredUploadSessions: %v", err)
	}
	if len(expired) != 1 || expired[0].UploadID != "upl_old" {
		t.Errorf("expired = %+v, want [upl_old]", expired)
	}
}

func TestTokens_CRUD(t *testing.T) {
	s, ctx := newStore(t)

	tok := &APIToken{Name: "ci", TokenHash: "deadbeef", TokenPrefix: "tok_dead", Scopes: []byte(`["admin"]`)}
	if err := s.CreateToken(ctx, tok); err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	got, err := s.GetTokenByHash(ctx, "deadbeef")
	if err != nil || got.Name != "ci" {
		t.Fatalf("GetTokenByHash: %v (%+v)", err, got)
	}

	if err := s.TouchTokenUsed(ctx, tok.ID, fixedNow); err != nil {
		t.Fatalf("TouchTokenUsed: %v", err)
	}
	if err := s.RevokeToken(ctx, tok.ID, fixedNow); err != nil {
		t.Fatalf("RevokeToken: %v", err)
	}
	got, _ = s.GetTokenByHash(ctx, "deadbeef")
	if got.LastUsedAt == nil || got.RevokedAt == nil {
		t.Errorf("touch/revoke not persisted: %+v", got)
	}

	list, _ := s.ListTokens(ctx)
	if len(list) != 1 {
		t.Errorf("ListTokens len = %d, want 1", len(list))
	}
}

func TestAudit_AppendAndList(t *testing.T) {
	s, ctx := newStore(t)

	actor := "tok_ci"
	target := "demo/report.pdf"
	a := &AuditLog{
		Actor: &actor, Action: "file.delete", Target: &target,
		Details: []byte(`{"reason":"expired"}`),
	}
	if err := s.AppendAudit(ctx, a); err != nil {
		t.Fatalf("AppendAudit: %v", err)
	}
	if a.ID == 0 || !a.Timestamp.Equal(fixedNow) {
		t.Errorf("audit not stamped: %+v", a)
	}

	list, err := s.ListAudit(ctx, 10)
	if err != nil {
		t.Fatalf("ListAudit: %v", err)
	}
	if len(list) != 1 || list[0].Action != "file.delete" || list[0].Actor == nil {
		t.Errorf("ListAudit = %+v", list)
	}
}
