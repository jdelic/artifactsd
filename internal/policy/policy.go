// Package policy makes access decisions for public serve routes by combining
// effective visibility, configured auth, and expiry (design/03 §5, design/14).
// It encodes the existence-non-disclosure rules (private/expired → 404) and the
// per-auth-type challenge (oidc → basic → bearer).
package policy

import (
	"net/http"
	"strings"

	"github.com/jdelic/artifactsd/internal/auth"
	"github.com/jdelic/artifactsd/internal/meta"
)

// Outcome is the high-level result of a policy decision.
type Outcome int

const (
	// Serve: the request may be served.
	Serve Outcome = iota
	// NotFound: 404 — used for private/expired/bearer-hidden/listing-off so
	// existence is never disclosed (SPEC §17).
	NotFound
	// Unauthorized: 401 with a WWW-Authenticate challenge.
	Unauthorized
	// Forbidden: 403 — authenticated but not permitted (reserved; object policy
	// generally prefers 404/401).
	Forbidden
	// RedirectOIDC: 302 to the OIDC authorization endpoint (browser request).
	RedirectOIDC
)

// Decision is the result of Decide.
type Decision struct {
	Outcome Outcome
	// Challenge is the WWW-Authenticate header value for Unauthorized outcomes.
	Challenge string
	// Subject and Scheme identify the authenticated principal when an
	// auth-required object is served (for audit logging, decision N6).
	Subject string
	Scheme  string
}

// Input is everything Decide needs about one request against one resolved object.
type Input struct {
	// Effective is the resolved metadata for the object (after merge).
	Effective *meta.Effective
	// Expired is true if the object's effective expiry has passed.
	Expired bool
	// AncestorPrivate is true if any ancestor resolves to private (ancestor
	// guard, design/03 §5): the object is then treated as private.
	AncestorPrivate bool
	// IsDirectory is true for a directory (trailing-slash) request.
	IsDirectory bool
	// Request is the incoming HTTP request (for auth + Accept negotiation).
	Request *http.Request
	// OIDC, when set, enables verification of OIDC auth entries (issuer
	// discovery + JWKS). When nil, OIDC entries cannot authenticate and only
	// drive the challenge (redirect/401).
	OIDC *auth.OIDCProviderCache
}

// Decide evaluates the access policy for a public serve route.
func Decide(in Input) Decision {
	eff := in.Effective

	// 1. Private (or under a private ancestor) → 404, never reveal existence.
	if in.AncestorPrivate || eff.Visibility == meta.VisibilityPrivate {
		return Decision{Outcome: NotFound}
	}

	// 2. Expired → 404 (not 403).
	if in.Expired {
		return Decision{Outcome: NotFound}
	}

	// 3. Auth-required → verify any-of, else challenge by type.
	if eff.RequiresAuth() {
		verifiers := compileVerifiers(eff.Auth, in.OIDC)
		if ok, subject, scheme := auth.Authenticate(verifiers, in.Request); ok {
			return decideDirectory(in, Decision{Outcome: Serve, Subject: subject, Scheme: scheme})
		}
		return challenge(eff.Auth, in.Request)
	}

	// 4 & 5. Public/unlisted with no auth → serve (subject to directory rules).
	return decideDirectory(in, Decision{Outcome: Serve})
}

// decideDirectory applies the directory-listing rule (design/03 §5.5): a
// directory request is only allowed when directory_listing is enabled; a file
// request is unaffected.
func decideDirectory(in Input, served Decision) Decision {
	if in.IsDirectory && !in.Effective.DirectoryListing {
		return Decision{Outcome: NotFound}
	}
	return served
}

// challenge picks the response for an unauthenticated auth-required object by
// auth-type precedence oidc → basic → bearer (design/03 §5, decision C4).
func challenge(entries []meta.AuthEntry, r *http.Request) Decision {
	var hasOIDC, hasBasic, hasBearer bool
	for i := range entries {
		switch entries[i].Type {
		case meta.AuthOIDC:
			hasOIDC = true
		case meta.AuthBasic:
			hasBasic = true
		case meta.AuthBearer:
			hasBearer = true
		}
	}

	switch {
	case hasOIDC:
		// Browsers get redirected to the IdP; API clients get a 401 Bearer.
		if isBrowser(r) {
			return Decision{Outcome: RedirectOIDC}
		}
		return Decision{Outcome: Unauthorized, Challenge: "Bearer"}
	case hasBasic:
		return Decision{Outcome: Unauthorized, Challenge: `Basic realm="` + auth.DefaultRealm + `"`}
	case hasBearer:
		// Bearer-only is capability-style: hide existence from the unauthorized.
		return Decision{Outcome: NotFound}
	default:
		// Auth configured but no recognized type — fail closed.
		return Decision{Outcome: NotFound}
	}
}

// compileVerifiers turns effective auth entries into request verifiers. OIDC
// entries compile to a verifier only when an OIDCProviderCache is supplied
// (issuer discovery requires network); without one they still drive the
// challenge but cannot authenticate.
func compileVerifiers(entries []meta.AuthEntry, oidcCache *auth.OIDCProviderCache) []auth.Verifier {
	verifiers := make([]auth.Verifier, 0, len(entries))
	for i := range entries {
		e := &entries[i]
		switch e.Type {
		case meta.AuthBearer:
			if e.TokenHash != "" {
				verifiers = append(verifiers, auth.NewBearerVerifier(e.TokenHash))
			}
		case meta.AuthBasic:
			if e.Username != "" && e.PasswordHash != "" {
				verifiers = append(verifiers, auth.NewBasicVerifier(e.Username, e.PasswordHash, ""))
			}
		case meta.AuthOIDC:
			if oidcCache != nil && e.Issuer != "" {
				verifiers = append(verifiers, auth.NewOIDCVerifier(&auth.OIDCConfig{
					Issuer:                    e.Issuer,
					Audience:                  e.Audience,
					AllowedSubjects:           e.AllowedSubjects,
					AllowedGroups:             e.AllowedGroups,
					GroupClaim:                e.GroupClaim,
					InsecureSkipAudienceCheck: e.InsecureSkipAudienceCheck,
					AnyAuthenticated:          e.AnyAuthenticated,
				}, oidcCache))
			}
		}
	}
	return verifiers
}

// isBrowser reports whether the request prefers an HTML response.
func isBrowser(r *http.Request) bool {
	return r != nil && strings.Contains(r.Header.Get("Accept"), "text/html")
}
