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
	// Forbidden: 403 — authenticated but not permitted: a signed-in viewer
	// whose identity the object's oidc entries don't admit (artifactsd-m9e.22).
	// Subject names who is signed in.
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
	// ViewerLogin describes the server's own OIDC login; nil when OIDC is not
	// configured. Only oidc entries for this issuer and client can be satisfied
	// by a viewer session or trigger the browser login (artifactsd-m9e.22).
	ViewerLogin *ViewerLogin
	// Viewer is the identity from a valid viewer session cookie, if any.
	Viewer *ViewerIdentity
}

// ViewerLogin is the server's OIDC client, as far as object access cares.
type ViewerLogin struct {
	Issuer   string
	ClientID string
	// GroupClaim is the claim groups were read from at login.
	GroupClaim string
}

// ViewerIdentity is a person signed in through the browser login.
type ViewerIdentity struct {
	Subject string
	Email   string
	Groups  []string
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

	// 2b. Protected but no auth configured → 404: nothing can satisfy the
	// requirement, so fail closed instead of serving it like public.
	if eff.ProtectedWithoutAuth() {
		return Decision{Outcome: NotFound}
	}

	// 3. Auth-required → verify any-of, else challenge by type.
	if eff.RequiresAuth() {
		eligible := viewerEntries(eff.Auth, in.ViewerLogin)
		if in.Viewer != nil {
			for _, e := range eligible {
				if ok, subject := oidcConfigFor(e).AuthorizesIdentity(
					in.Viewer.Subject, in.Viewer.Email, in.Viewer.Groups, in.ViewerLogin.GroupClaim); ok {
					return decideDirectory(in, Decision{Outcome: Serve, Subject: subject, Scheme: "OIDC"})
				}
			}
		}
		verifiers := compileVerifiers(eff.Auth, in.OIDC)
		if ok, subject, scheme := auth.Authenticate(verifiers, in.Request); ok {
			return decideDirectory(in, Decision{Outcome: Serve, Subject: subject, Scheme: scheme})
		}
		if in.Viewer != nil && len(eligible) > 0 {
			// Signed in, but not admitted: say so instead of sending the
			// browser back to the IdP, which would loop under SSO.
			who := in.Viewer.Email
			if who == "" {
				who = in.Viewer.Subject
			}
			return Decision{Outcome: Forbidden, Subject: who}
		}
		return challenge(eff.Auth, in.Request, len(eligible) > 0)
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
func challenge(entries []meta.AuthEntry, r *http.Request, browserLogin bool) Decision {
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
		// Browsers are sent to the viewer login, but only when an entry can
		// be satisfied by it (the server's own issuer/client) and the request
		// is a plain read; everyone else gets a 401 Bearer challenge.
		if browserLogin && isBrowser(r) && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
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
// entries compile to a verifier only when an OIDCProviderCache is supplied and
// allows their issuer (issuer discovery requires network); otherwise they still
// drive the challenge but cannot authenticate.
func compileVerifiers(entries []meta.AuthEntry, oidcCache *auth.OIDCProviderCache) []auth.Verifier {
	verifiers := make([]auth.Verifier, 0, len(entries))
	// All basic entries share one verifier so a request costs one argon2 run
	// however many entries there are (artifactsd-m9e.9).
	var basic []auth.BasicCredential
	for i := range entries {
		e := &entries[i]
		switch e.Type {
		case meta.AuthBearer:
			if e.TokenHash != "" {
				verifiers = append(verifiers, auth.NewBearerVerifier(e.TokenHash))
			}
		case meta.AuthBasic:
			if e.Username != "" && e.PasswordHash != "" {
				basic = append(basic, auth.BasicCredential{Username: e.Username, PasswordHash: e.PasswordHash})
			}
		case meta.AuthOIDC:
			// Entries naming a non-allowlisted issuer (e.g. stored before the
			// allowlist existed) compile to nothing: the object stays
			// auth-required and fails closed with the normal challenge, and no
			// discovery request is ever made (artifactsd-m9e.21).
			if oidcCache != nil && e.Issuer != "" && oidcCache.IssuerAllowed(e.Issuer) {
				verifiers = append(verifiers, auth.NewOIDCVerifier(oidcConfigFor(e), oidcCache))
			}
		}
	}
	if len(basic) > 0 {
		verifiers = append(verifiers, auth.NewBasicUsersVerifier(basic, ""))
	}
	return verifiers
}

// isBrowser reports whether the request prefers an HTML response.
func isBrowser(r *http.Request) bool {
	return r != nil && strings.Contains(r.Header.Get("Accept"), "text/html")
}

// oidcConfigFor converts an oidc metadata entry to an auth.OIDCConfig.
func oidcConfigFor(e *meta.AuthEntry) *auth.OIDCConfig {
	return &auth.OIDCConfig{
		Issuer:                    e.Issuer,
		Audience:                  e.Audience,
		AllowedSubjects:           e.AllowedSubjects,
		AllowedGroups:             e.AllowedGroups,
		GroupClaim:                e.GroupClaim,
		InsecureSkipAudienceCheck: e.InsecureSkipAudienceCheck,
		AnyAuthenticated:          e.AnyAuthenticated,
	}
}

// viewerEntries returns the oidc entries a viewer session (the server's own
// OIDC login) can satisfy: same issuer, and the server's client id as audience
// unless the entry skips the audience check.
func viewerEntries(entries []meta.AuthEntry, login *ViewerLogin) []*meta.AuthEntry {
	if login == nil || login.Issuer == "" || login.ClientID == "" {
		return nil
	}
	var out []*meta.AuthEntry
	for i := range entries {
		e := &entries[i]
		if e.Type != meta.AuthOIDC || auth.NormalizeIssuer(e.Issuer) != auth.NormalizeIssuer(login.Issuer) {
			continue
		}
		if e.Audience == login.ClientID || e.InsecureSkipAudienceCheck {
			out = append(out, e)
		}
	}
	return out
}
