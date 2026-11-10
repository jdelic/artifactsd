package store

import (
	"context"
	"testing"
	"time"
)

// Regression tests for artifactsd-6b4: delete_after_last_access must count
// from the last access or from when the rule was set, never from creation for
// an object that was created long before the rule.

const day = 24 * time.Hour

// clockStore returns a migrated store whose clock the test can move.
func clockStore(t *testing.T) (*Store, context.Context, *time.Time) {
	t.Helper()
	s, ctx := newStore(t)
	now := fixedNow
	s.WithClock(func() time.Time { return now })
	return s, ctx, &now
}

func mustExpiry(t *testing.T, got *time.Time, want time.Time) {
	t.Helper()
	if got == nil || !got.Equal(want) {
		t.Fatalf("expires_at = %v, want %v", got, want)
	}
}

func TestIdleExpiry_CollectionRuleSetLaterCountsFromNow(t *testing.T) {
	s, ctx, now := clockStore(t)
	c := mustCollection(t, s, ctx, "old")

	*now = now.Add(90 * day) // the collection is 90 days old when the rule is set
	c.Metadata = []byte(`{"expiry":{"delete_after_last_access":"30d"}}`)
	if err := s.UpdateCollectionMetadata(ctx, c); err != nil {
		t.Fatalf("UpdateCollectionMetadata: %v", err)
	}
	if err := s.RecomputeCollectionDenormalized(ctx, c.ID, "private"); err != nil {
		t.Fatalf("Recompute: %v", err)
	}
	got, _ := s.GetCollection(ctx, c.ID)
	mustExpiry(t, got.ExpiresAt, now.Add(30*day))
}

func TestIdleExpiry_CollectionAccessMovesExpiry(t *testing.T) {
	s, ctx, now := clockStore(t)
	c := &Collection{Name: "c", Metadata: []byte(`{"expiry":{"delete_after_last_access":"30d"}}`)}
	if err := s.CreateCollection(ctx, c); err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}

	*now = now.Add(20 * day)
	if err := s.TouchCollectionLastAccess(ctx, c.ID, *now); err != nil {
		t.Fatalf("TouchCollectionLastAccess: %v", err)
	}
	if err := s.RecomputeCollectionDenormalized(ctx, c.ID, "private"); err != nil {
		t.Fatalf("Recompute: %v", err)
	}
	got, _ := s.GetCollection(ctx, c.ID)
	mustExpiry(t, got.ExpiresAt, now.Add(30*day))
}

func TestIdleExpiry_UnrelatedMetadataEditKeepsClock(t *testing.T) {
	s, ctx, now := clockStore(t)
	c := &Collection{Name: "c", Metadata: []byte(`{"expiry":{"delete_after_last_access":"30d"}}`)}
	if err := s.CreateCollection(ctx, c); err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}
	created := *now

	*now = now.Add(10 * day)
	c.Metadata = []byte(`{"access":{"visibility":"public"},"expiry":{"delete_after_last_access":"30d"}}`)
	if err := s.UpdateCollectionMetadata(ctx, c); err != nil {
		t.Fatalf("UpdateCollectionMetadata: %v", err)
	}
	if err := s.RecomputeCollectionDenormalized(ctx, c.ID, "private"); err != nil {
		t.Fatalf("Recompute: %v", err)
	}
	got, _ := s.GetCollection(ctx, c.ID)
	if got.LastAccessAt != nil {
		t.Errorf("last_access_at = %v, want nil (rule unchanged, nothing served)", got.LastAccessAt)
	}
	mustExpiry(t, got.ExpiresAt, created.Add(30*day))
}

func TestIdleExpiry_NodeRuleSetOrChangedCountsFromNow(t *testing.T) {
	s, ctx, now := clockStore(t)
	c := mustCollection(t, s, ctx, "c")
	n := &Node{CollectionID: c.ID, Path: "f", Name: "f", Kind: KindFile}
	if err := s.UpsertNode(ctx, n); err != nil {
		t.Fatalf("UpsertNode: %v", err)
	}

	// Never fetched, 40 days old, rule added now.
	*now = now.Add(40 * day)
	n.Metadata = []byte(`{"expiry":{"delete_after_last_access":"30d"}}`)
	if err := s.UpsertNode(ctx, n); err != nil {
		t.Fatalf("UpsertNode: %v", err)
	}
	if err := s.RecomputeNodeDenormalized(ctx, n.ID); err != nil {
		t.Fatalf("Recompute: %v", err)
	}
	got, _ := s.GetNodeByID(ctx, n.ID)
	mustExpiry(t, got.ExpiresAt, now.Add(30*day))

	// Last fetched 25 days ago; tightening the rule to 7d restarts the clock
	// instead of making the node due immediately.
	*now = now.Add(25 * day)
	got.Metadata = []byte(`{"expiry":{"delete_after_last_access":"7d"}}`)
	if err := s.UpsertNode(ctx, got); err != nil {
		t.Fatalf("UpsertNode: %v", err)
	}
	if err := s.RecomputeNodeDenormalized(ctx, n.ID); err != nil {
		t.Fatalf("Recompute: %v", err)
	}
	got, _ = s.GetNodeByID(ctx, n.ID)
	mustExpiry(t, got.ExpiresAt, now.Add(7*day))
}

func TestIdleExpiry_Share(t *testing.T) {
	s, ctx, now := clockStore(t)
	c := mustCollection(t, s, ctx, "c")
	sh := &Share{ShareID: "s1", CollectionID: c.ID, TargetKind: "collection"}
	if err := s.CreateShare(ctx, sh); err != nil {
		t.Fatalf("CreateShare: %v", err)
	}

	*now = now.Add(60 * day)
	sh.Metadata = []byte(`{"expiry":{"delete_after_last_access":"30d"}}`)
	if err := s.UpdateShare(ctx, sh); err != nil {
		t.Fatalf("UpdateShare: %v", err)
	}
	mustExpiry(t, sh.ExpiresAt, now.Add(30*day))

	*now = now.Add(10 * day)
	if err := s.TouchShareLastAccess(ctx, sh.ID, *now); err != nil {
		t.Fatalf("TouchShareLastAccess: %v", err)
	}
	if err := s.RecomputeShareDenormalized(ctx, sh.ID); err != nil {
		t.Fatalf("RecomputeShareDenormalized: %v", err)
	}
	got, _ := s.GetShare(ctx, "s1")
	mustExpiry(t, got.ExpiresAt, now.Add(30*day))
}

// A database created before migration 0004 with an old collection whose idle
// rule had already put expires_at in the past must come out of migration +
// startup maintenance with a future expiry.
func TestIdleExpiry_MigrationRescuesLegacyRows(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if err := ensureMigrationsTable(ctx, db.sql); err != nil {
		t.Fatal(err)
	}
	all, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range all {
		if m.version < 4 {
			if err := applyMigration(ctx, db.sql, m); err != nil {
				t.Fatalf("apply %d: %v", m.version, err)
			}
		}
	}

	created := time.Now().UTC().Add(-90 * day)
	past := created.Add(30 * day)
	idle := `{"expiry":{"delete_after_last_access":"30d"}}`
	res, err := db.sql.ExecContext(ctx,
		`INSERT INTO collections (name, metadata, created_at, updated_at, visibility, expires_at)
		 VALUES ('legacy', ?, ?, ?, 'private', ?)`, idle, fmtTime(created), fmtTime(created), fmtTime(past))
	if err != nil {
		t.Fatal(err)
	}
	collID, _ := res.LastInsertId()
	if _, err := db.sql.ExecContext(ctx,
		`INSERT INTO nodes (collection_id, path, name, kind, metadata, created_at, updated_at, expires_at)
		 VALUES (?, 'f', 'f', 'folder', ?, ?, ?, ?)`, collID, idle, fmtTime(created), fmtTime(created), fmtTime(past)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.sql.ExecContext(ctx,
		`INSERT INTO shares (share_id, collection_id, target_path, target_kind, metadata, created_at, expires_at)
		 VALUES ('s1', ?, '', 'collection', ?, ?, ?)`, collID, idle, fmtTime(created), fmtTime(past)); err != nil {
		t.Fatal(err)
	}

	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	s := New(db)
	n, err := s.RunPendingMaintenance(ctx, "private")
	if err != nil {
		t.Fatalf("RunPendingMaintenance: %v", err)
	}
	if n != 3 {
		t.Errorf("recomputed %d rows, want 3", n)
	}

	floor := time.Now().UTC().Add(29 * day)
	c, _ := s.GetCollection(ctx, collID)
	node, _ := s.GetNode(ctx, collID, "f")
	sh, _ := s.GetShare(ctx, "s1")
	for name, exp := range map[string]*time.Time{"collection": c.ExpiresAt, "node": node.ExpiresAt, "share": sh.ExpiresAt} {
		if exp == nil || exp.Before(floor) {
			t.Errorf("%s expires_at = %v, want ≥ %v", name, exp, floor)
		}
	}

	// The flag is cleared: a second startup does nothing.
	if n, err := s.RunPendingMaintenance(ctx, "private"); err != nil || n != 0 {
		t.Errorf("second RunPendingMaintenance = (%d, %v), want (0, nil)", n, err)
	}
}
