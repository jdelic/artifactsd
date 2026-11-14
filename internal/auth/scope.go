package auth

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Scope is a management-API permission carried by an API token (design/04 §1).
type Scope string

// Known scopes. `admin` is a superscope granting everything; `<resource>:write`
// and `<resource>:delete` each imply `<resource>:read`, but neither implies the
// other: deleting is an explicit permission, so a leaked write token cannot
// wipe collections (artifactsd-m9e.25). Token management, upload requests,
// audit, and maintenance are admin-only (design/04 §8): there are deliberately
// no tokens:* scopes, since a token that can mint tokens can mint an admin one.
const (
	ScopeAdmin             Scope = "admin"
	ScopeCollectionsRead   Scope = "collections:read"
	ScopeCollectionsWrite  Scope = "collections:write"
	ScopeCollectionsDelete Scope = "collections:delete"
	ScopeSharesRead        Scope = "shares:read"
	ScopeSharesWrite       Scope = "shares:write"
	ScopeSharesDelete      Scope = "shares:delete"
)

// KnownScopes lists every scope a token may be granted (design/04 §1). Used to
// reject unknown/typo'd scopes when minting tokens.
var KnownScopes = []Scope{
	ScopeAdmin,
	ScopeCollectionsRead, ScopeCollectionsWrite, ScopeCollectionsDelete,
	ScopeSharesRead, ScopeSharesWrite, ScopeSharesDelete,
}

// IsKnownScope reports whether sc is a recognized scope.
func IsKnownScope(sc Scope) bool {
	for _, k := range KnownScopes {
		if k == sc {
			return true
		}
	}
	return false
}

// IsGrantScope reports whether sc may appear on a per-collection grant
// (design/16 §2): any known scope except admin.
func IsGrantScope(sc Scope) bool {
	return sc != ScopeAdmin && IsKnownScope(sc)
}

// ScopeSet is a token's granted scopes.
type ScopeSet map[Scope]struct{}

// NewScopeSet builds a set from explicit scopes.
func NewScopeSet(scopes ...Scope) ScopeSet {
	s := make(ScopeSet, len(scopes))
	for _, sc := range scopes {
		s[sc] = struct{}{}
	}
	return s
}

// ParseScopes parses a JSON array of scope strings (the api_tokens.scopes column).
func ParseScopes(raw []byte) (ScopeSet, error) {
	if len(raw) == 0 {
		return ScopeSet{}, nil
	}
	var arr []string
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil, fmt.Errorf("auth: parsing scopes: %w", err)
	}
	set := make(ScopeSet, len(arr))
	for _, s := range arr {
		set[Scope(strings.TrimSpace(s))] = struct{}{}
	}
	return set, nil
}

// Has reports exact membership.
func (s ScopeSet) Has(sc Scope) bool {
	_, ok := s[sc]
	return ok
}

// Satisfies reports whether the set grants the required scope. `admin` grants
// everything; `<resource>:write` and `<resource>:delete` each grant the
// matching `<resource>:read`; nothing else is implied.
func (s ScopeSet) Satisfies(required Scope) bool {
	if s.Has(ScopeAdmin) || s.Has(required) {
		return true
	}
	if res, ok := strings.CutSuffix(string(required), ":read"); ok {
		return s.Has(Scope(res+":write")) || s.Has(Scope(res+":delete"))
	}
	return false
}

// Strings returns the scopes as a sorted-free string slice (for serialization).
func (s ScopeSet) Strings() []string {
	out := make([]string, 0, len(s))
	for sc := range s {
		out = append(out, string(sc))
	}
	return out
}
