package meta

import (
	"fmt"
	"strings"

	"github.com/jdelic/artifactsd/internal/durations"
)

// Validate checks the document for well-formedness at write time: valid
// visibility, auth entry shapes, and parseable expiry durations. Header safety
// is enforced separately (design/03 §6). It does not require secrets to be
// hashed.
func (d *Document) Validate() error {
	var probs []string
	add := func(format string, args ...any) { probs = append(probs, fmt.Sprintf(format, args...)) }

	if d.Content != nil {
		for _, name := range RejectedHeaders(d.Content.Headers) {
			add("content.headers %q is not allowed (hop-by-hop/unsafe)", name)
		}
	}

	if d.Access != nil {
		if d.Access.Visibility != nil && !validVisibility(*d.Access.Visibility) {
			add("access.visibility %q is invalid", *d.Access.Visibility)
		}
		if d.Access.Auth != nil {
			entries := *d.Access.Auth
			for i := range entries {
				validateAuthEntry(i, &entries[i], add)
			}
		}
	}

	if d.Expiry != nil {
		validateDuration("expiry.delete_after_creation", d.Expiry.DeleteAfterCreation, add)
		validateDuration("expiry.delete_after_last_access", d.Expiry.DeleteAfterLastAccess, add)
		if d.Expiry.ExpiresAt != nil {
			if _, err := ParseInstant(*d.Expiry.ExpiresAt); err != nil {
				add("expiry.expires_at %q is not an RFC3339 time", *d.Expiry.ExpiresAt)
			}
		}
	}

	if len(probs) == 0 {
		return nil
	}
	return fmt.Errorf("invalid metadata: %s", strings.Join(probs, "; "))
}

func validateAuthEntry(i int, e *AuthEntry, add func(string, ...any)) {
	switch e.Type {
	case AuthBearer:
		if e.Token == "" && e.TokenHash == "" {
			add("auth[%d] (bearer) requires token or token_hash", i)
		}
	case AuthBasic:
		if e.Username == "" {
			add("auth[%d] (basic) requires username", i)
		}
		if e.Password == "" && e.PasswordHash == "" {
			add("auth[%d] (basic) requires password or password_hash", i)
		}
	case AuthOIDC:
		if e.Issuer == "" {
			add("auth[%d] (oidc) requires issuer", i)
		}
		if len(e.AllowedSubjects) == 0 && len(e.AllowedGroups) == 0 && !e.AnyAuthenticated {
			add("auth[%d] (oidc) requires allowed_subjects, allowed_groups, or any_authenticated", i)
		}
	case "":
		add("auth[%d] missing type", i)
	default:
		add("auth[%d] has unknown type %q", i, e.Type)
	}
}

func validateDuration(field string, val *string, add func(string, ...any)) {
	if val == nil || strings.TrimSpace(*val) == "" {
		return
	}
	if _, err := durations.Parse(*val); err != nil {
		add("%s %q is not a valid duration", field, *val)
	}
}

func validVisibility(v string) bool {
	switch v {
	case VisibilityPublic, VisibilityUnlisted, VisibilityProtected, VisibilityPrivate:
		return true
	default:
		return false
	}
}
