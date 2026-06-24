package store

import (
	"encoding/json"
	"testing"
	"time"
)

func TestDeriveDenormalized(t *testing.T) {
	created := time.Date(2026, 6, 23, 0, 0, 0, 0, time.UTC)

	t.Run("empty", func(t *testing.T) {
		vis, exp, err := DeriveDenormalized(nil, created, time.Time{})
		if err != nil || vis != "" || exp != nil {
			t.Fatalf("got (%q,%v,%v)", vis, exp, err)
		}
	})

	t.Run("visibility only", func(t *testing.T) {
		vis, exp, err := DeriveDenormalized(json.RawMessage(`{"access":{"visibility":"public"}}`), created, time.Time{})
		if err != nil || vis != "public" || exp != nil {
			t.Fatalf("got (%q,%v,%v)", vis, exp, err)
		}
	})

	t.Run("explicit expires_at", func(t *testing.T) {
		_, exp, err := DeriveDenormalized(json.RawMessage(`{"expiry":{"expires_at":"2026-07-01T00:00:00Z"}}`), created, time.Time{})
		if err != nil || exp == nil || !exp.Equal(time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)) {
			t.Fatalf("got (%v,%v)", exp, err)
		}
	})

	t.Run("delete_after_creation", func(t *testing.T) {
		_, exp, err := DeriveDenormalized(json.RawMessage(`{"expiry":{"delete_after_creation":"7d"}}`), created, time.Time{})
		if err != nil || exp == nil || !exp.Equal(created.Add(7*24*time.Hour)) {
			t.Fatalf("got (%v,%v)", exp, err)
		}
	})

	t.Run("delete_after_last_access falls back to created_at when never accessed", func(t *testing.T) {
		_, exp, err := DeriveDenormalized(json.RawMessage(`{"expiry":{"delete_after_last_access":"5d"}}`), created, time.Time{})
		if err != nil || exp == nil || !exp.Equal(created.Add(5*24*time.Hour)) {
			t.Fatalf("got (%v,%v)", exp, err)
		}
	})

	t.Run("delete_after_last_access relative to lastAccess once accessed", func(t *testing.T) {
		lastAccess := created.Add(2 * 24 * time.Hour)
		_, exp, err := DeriveDenormalized(json.RawMessage(`{"expiry":{"delete_after_last_access":"5d"}}`), created, lastAccess)
		if err != nil || exp == nil || !exp.Equal(lastAccess.Add(5*24*time.Hour)) {
			t.Fatalf("got (%v,%v)", exp, err)
		}
	})

	t.Run("earliest wins", func(t *testing.T) {
		// explicit is later than creation+3d → creation+3d wins.
		meta := json.RawMessage(`{"expiry":{"expires_at":"2026-12-01T00:00:00Z","delete_after_creation":"3d"}}`)
		_, exp, err := DeriveDenormalized(meta, created, time.Time{})
		if err != nil || exp == nil || !exp.Equal(created.Add(3*24*time.Hour)) {
			t.Fatalf("got (%v,%v)", exp, err)
		}
	})

	t.Run("earliest wins including delete_after_last_access", func(t *testing.T) {
		// delete_after_creation (created+3d) is earlier than delete_after_last_access
		// (lastAccess+5d == created+7d) → creation+3d wins.
		lastAccess := created.Add(2 * 24 * time.Hour)
		meta := json.RawMessage(`{"expiry":{"delete_after_creation":"3d","delete_after_last_access":"5d"}}`)
		_, exp, err := DeriveDenormalized(meta, created, lastAccess)
		if err != nil || exp == nil || !exp.Equal(created.Add(3*24*time.Hour)) {
			t.Fatalf("got (%v,%v)", exp, err)
		}
	})

	t.Run("bad duration", func(t *testing.T) {
		if _, _, err := DeriveDenormalized(json.RawMessage(`{"expiry":{"delete_after_creation":"soon"}}`), created, time.Time{}); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("bad delete_after_last_access duration", func(t *testing.T) {
		if _, _, err := DeriveDenormalized(json.RawMessage(`{"expiry":{"delete_after_last_access":"soon"}}`), created, time.Time{}); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestRecomputeDenormalized_PersistsColumns(t *testing.T) {
	s, ctx := newStore(t)

	// Collection with metadata that sets visibility + creation-relative expiry.
	c := &Collection{Name: "demo", Metadata: []byte(`{"access":{"visibility":"public"},"expiry":{"delete_after_creation":"1d"}}`)}
	if err := s.CreateCollection(ctx, c); err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}
	if err := s.RecomputeCollectionDenormalized(ctx, c.ID, "private"); err != nil {
		t.Fatalf("RecomputeCollectionDenormalized: %v", err)
	}
	got, _ := s.GetCollection(ctx, c.ID)
	if got.Visibility != "public" {
		t.Errorf("visibility = %q, want public", got.Visibility)
	}
	if got.ExpiresAt == nil || !got.ExpiresAt.Equal(fixedNow.Add(24*time.Hour)) {
		t.Errorf("expires_at = %v, want %v", got.ExpiresAt, fixedNow.Add(24*time.Hour))
	}

	// Node with no own visibility keeps NULL (inherit); expiry derived.
	n := &Node{CollectionID: c.ID, Path: "f", Name: "f", Kind: KindFile, Metadata: []byte(`{"expiry":{"delete_after_creation":"2d"}}`)}
	if err := s.UpsertNode(ctx, n); err != nil {
		t.Fatalf("UpsertNode: %v", err)
	}
	if err := s.RecomputeNodeDenormalized(ctx, n.ID); err != nil {
		t.Fatalf("RecomputeNodeDenormalized: %v", err)
	}
	gotNode, _ := s.GetNodeByID(ctx, n.ID)
	if gotNode.Visibility != nil {
		t.Errorf("node visibility = %v, want nil (inherit)", *gotNode.Visibility)
	}
	if gotNode.ExpiresAt == nil || !gotNode.ExpiresAt.Equal(fixedNow.Add(48*time.Hour)) {
		t.Errorf("node expires_at = %v, want %v", gotNode.ExpiresAt, fixedNow.Add(48*time.Hour))
	}

	// delete_after_last_access recomputes relative to the node's last_access_at
	// once TouchLastAccess has advanced it (design/08 §3).
	n2 := &Node{CollectionID: c.ID, Path: "g", Name: "g", Kind: KindFile, Metadata: []byte(`{"expiry":{"delete_after_last_access":"3d"}}`)}
	if err := s.UpsertNode(ctx, n2); err != nil {
		t.Fatalf("UpsertNode: %v", err)
	}
	if err := s.RecomputeNodeDenormalized(ctx, n2.ID); err != nil {
		t.Fatalf("RecomputeNodeDenormalized: %v", err)
	}
	gotNode2, _ := s.GetNodeByID(ctx, n2.ID)
	if gotNode2.ExpiresAt == nil || !gotNode2.ExpiresAt.Equal(fixedNow.Add(72*time.Hour)) {
		t.Errorf("node expires_at (never accessed) = %v, want %v", gotNode2.ExpiresAt, fixedNow.Add(72*time.Hour))
	}

	accessedAt := fixedNow.Add(time.Hour)
	if err := s.TouchLastAccess(ctx, n2.ID, accessedAt); err != nil {
		t.Fatalf("TouchLastAccess: %v", err)
	}
	if err := s.RecomputeNodeDenormalized(ctx, n2.ID); err != nil {
		t.Fatalf("RecomputeNodeDenormalized after touch: %v", err)
	}
	gotNode2, _ = s.GetNodeByID(ctx, n2.ID)
	if gotNode2.ExpiresAt == nil || !gotNode2.ExpiresAt.Equal(accessedAt.Add(72*time.Hour)) {
		t.Errorf("node expires_at (after touch) = %v, want %v", gotNode2.ExpiresAt, accessedAt.Add(72*time.Hour))
	}
}

func TestRecomputeAllDenormalized(t *testing.T) {
	s, ctx := newStore(t)
	c := &Collection{Name: "demo", Metadata: []byte(`{"access":{"visibility":"public"}}`)}
	_ = s.CreateCollection(ctx, c)
	_ = s.UpsertNode(ctx, &Node{CollectionID: c.ID, Path: "a", Name: "a", Kind: KindFile, Metadata: []byte(`{}`)})
	_ = s.UpsertNode(ctx, &Node{CollectionID: c.ID, Path: "b", Name: "b", Kind: KindFile, Metadata: []byte(`{}`)})

	n, err := s.RecomputeAllDenormalized(ctx, "private")
	if err != nil {
		t.Fatalf("RecomputeAllDenormalized: %v", err)
	}
	if n != 3 { // 1 collection + 2 nodes
		t.Errorf("recomputed %d rows, want 3", n)
	}
	got, _ := s.GetCollection(ctx, c.ID)
	if got.Visibility != "public" {
		t.Errorf("collection visibility = %q, want public", got.Visibility)
	}
}
