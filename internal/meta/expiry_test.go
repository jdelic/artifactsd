package meta

import (
	"testing"
	"time"
)

var (
	created  = time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	accessed = time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)
)

func expiryDoc(e *Expiry) *Document { return &Document{Expiry: e} }

func TestResolveOwnExpiry_None(t *testing.T) {
	got, err := ResolveOwnExpiry(&Document{}, created, accessed)
	if err != nil || got != nil {
		t.Fatalf("got (%v,%v), want (nil,nil)", got, err)
	}
}

func TestResolveOwnExpiry_Absolute(t *testing.T) {
	got, err := ResolveOwnExpiry(expiryDoc(&Expiry{ExpiresAt: strp("2026-07-01T00:00:00Z")}), created, accessed)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	if got == nil || !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestResolveOwnExpiry_AfterCreation(t *testing.T) {
	got, _ := ResolveOwnExpiry(expiryDoc(&Expiry{DeleteAfterCreation: strp("7d")}), created, accessed)
	if got == nil || !got.Equal(created.Add(7*24*time.Hour)) {
		t.Errorf("got %v, want created+7d", got)
	}
}

func TestResolveOwnExpiry_AfterLastAccess(t *testing.T) {
	got, _ := ResolveOwnExpiry(expiryDoc(&Expiry{DeleteAfterLastAccess: strp("30d")}), created, accessed)
	if got == nil || !got.Equal(accessed.Add(30*24*time.Hour)) {
		t.Errorf("got %v, want lastAccess+30d", got)
	}
}

func TestResolveOwnExpiry_LastAccessFallsBackToCreation(t *testing.T) {
	got, _ := ResolveOwnExpiry(expiryDoc(&Expiry{DeleteAfterLastAccess: strp("5d")}), created, time.Time{})
	if got == nil || !got.Equal(created.Add(5*24*time.Hour)) {
		t.Errorf("got %v, want created+5d (no last access)", got)
	}
}

func TestResolveOwnExpiry_EarliestOfMultiple(t *testing.T) {
	// expires_at far future, delete_after_creation 3d (earliest), last_access 30d.
	got, _ := ResolveOwnExpiry(expiryDoc(&Expiry{
		ExpiresAt:             strp("2026-12-01T00:00:00Z"),
		DeleteAfterCreation:   strp("3d"),
		DeleteAfterLastAccess: strp("30d"),
	}), created, accessed)
	if got == nil || !got.Equal(created.Add(3*24*time.Hour)) {
		t.Errorf("got %v, want created+3d (earliest)", got)
	}
}

func TestResolveOwnExpiry_BadDuration(t *testing.T) {
	if _, err := ResolveOwnExpiry(expiryDoc(&Expiry{DeleteAfterCreation: strp("soon")}), created, accessed); err == nil {
		t.Error("expected error for bad duration")
	}
}

func TestEffectiveExpiry_ParentWinsWhenEarlier(t *testing.T) {
	parent := created.Add(2 * 24 * time.Hour) // T+2d
	// Child wants T+10d, but parent expires at T+2d → child can't outlive it.
	got, err := EffectiveExpiry(expiryDoc(&Expiry{DeleteAfterCreation: strp("10d")}), created, accessed, &parent)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || !got.Equal(parent) {
		t.Errorf("got %v, want parent %v", got, parent)
	}
}

func TestEffectiveExpiry_ChildWinsWhenEarlier(t *testing.T) {
	parent := created.Add(10 * 24 * time.Hour)
	got, _ := EffectiveExpiry(expiryDoc(&Expiry{DeleteAfterCreation: strp("1d")}), created, accessed, &parent)
	if got == nil || !got.Equal(created.Add(24*time.Hour)) {
		t.Errorf("got %v, want child+1d", got)
	}
}

func TestEffectiveExpiry_InheritsParentWhenChildHasNone(t *testing.T) {
	parent := created.Add(5 * 24 * time.Hour)
	got, _ := EffectiveExpiry(&Document{}, created, accessed, &parent)
	if got == nil || !got.Equal(parent) {
		t.Errorf("got %v, want inherited parent %v", got, parent)
	}
}

func TestEarliest(t *testing.T) {
	a := created
	b := created.Add(time.Hour)
	if got := Earliest(&a, &b); !got.Equal(a) {
		t.Errorf("Earliest = %v, want %v", got, a)
	}
	if got := Earliest(nil, &b); !got.Equal(b) {
		t.Errorf("Earliest(nil,b) = %v, want b", got)
	}
	if got := Earliest(nil, nil); got != nil {
		t.Errorf("Earliest(nil,nil) = %v, want nil", got)
	}
}
