package auth

import "testing"

func TestScopeSet_Satisfies(t *testing.T) {
	cases := []struct {
		name     string
		set      ScopeSet
		required Scope
		want     bool
	}{
		{"admin grants all", NewScopeSet(ScopeAdmin), ScopeCollectionsWrite, true},
		{"exact match", NewScopeSet(ScopeCollectionsRead), ScopeCollectionsRead, true},
		{"write implies read", NewScopeSet(ScopeCollectionsWrite), ScopeCollectionsRead, true},
		{"read does not imply write", NewScopeSet(ScopeCollectionsRead), ScopeCollectionsWrite, false},
		{"unrelated scope", NewScopeSet(ScopeSharesWrite), ScopeCollectionsRead, false},
		{"empty set", NewScopeSet(), ScopeCollectionsRead, false},
		{"shares write implies shares read", NewScopeSet(ScopeSharesWrite), ScopeSharesRead, true},
	}
	for _, c := range cases {
		if got := c.set.Satisfies(c.required); got != c.want {
			t.Errorf("%s: Satisfies(%q) = %v, want %v", c.name, c.required, got, c.want)
		}
	}
}

func TestParseScopes(t *testing.T) {
	set, err := ParseScopes([]byte(`["admin","collections:read"]`))
	if err != nil {
		t.Fatalf("ParseScopes: %v", err)
	}
	if !set.Has(ScopeAdmin) || !set.Has(ScopeCollectionsRead) {
		t.Errorf("parsed set missing scopes: %v", set)
	}

	empty, err := ParseScopes(nil)
	if err != nil || len(empty) != 0 {
		t.Errorf("ParseScopes(nil) = %v, %v", empty, err)
	}

	if _, err := ParseScopes([]byte(`not json`)); err == nil {
		t.Error("expected error for invalid JSON")
	}
}
