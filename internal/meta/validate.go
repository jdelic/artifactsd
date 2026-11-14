package meta

import (
	"fmt"
	"strings"

	"github.com/jdelic/artifactsd/internal/durations"
)

// Validate is ValidateWith(nil): validation under the built-in defaults.
func (d *Document) Validate() error { return d.ValidateWith(nil) }

// ValidateWith checks the document for well-formedness at write time under
// the operator policy p (nil = built-in defaults): content.headers against the
// allowlist (design/03 §6), valid visibility, auth entry shapes, and parseable
// expiry durations. It does not require secrets to be hashed. Apply it to the
// incoming document or patch only — never to already-stored metadata, which
// may predate a rule and must stay readable and patchable.
func (d *Document) ValidateWith(p *Policy) error {
	var probs []string
	add := func(format string, args ...any) { probs = append(probs, fmt.Sprintf(format, args...)) }

	if d.Content != nil {
		probs = append(probs, p.headerProblems(d.Content.Headers)...)
	}

	if d.Access != nil {
		if d.Access.Visibility != nil && !validVisibility(*d.Access.Visibility) {
			add("access.visibility %q is invalid", *d.Access.Visibility)
		}
		if d.Access.Auth != nil {
			entries := *d.Access.Auth
			for i := range entries {
				validateAuthEntry(p, i, &entries[i], add)
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

func validateAuthEntry(p *Policy, i int, e *AuthEntry, add func(string, ...any)) {
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
		} else if !p.OIDCIssuerAllowed(e.Issuer) {
			// The server fetches discovery documents from the issuer URL, so
			// only operator-approved issuers may be named (artifactsd-m9e.21).
			add("auth[%d] (oidc) issuer %q is not allowed (ARTIFACTSD_OIDC_ALLOWED_ISSUERS)", i, e.Issuer)
		}
		// Audience (aud) must be verified so a token minted for another client at
		// the same issuer cannot satisfy this entry. Require an explicit audience
		// unless the operator deliberately opts out (design/06 §1.3, design/14
		// §2.3; artifactsd-bhq).
		if e.Audience == "" && !e.InsecureSkipAudienceCheck {
			add("auth[%d] (oidc) requires audience (or insecure_skip_audience_check)", i)
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
