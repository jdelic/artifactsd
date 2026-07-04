package main

import (
	"encoding/json"
	"strings"

	"github.com/spf13/pflag"
)

// visibilityFlags is the shared visibility flag group (design/10 §2): the four
// documented shorthand booleans plus the long-form --visibility. At most one
// of the five may be used per invocation.
type visibilityFlags struct {
	visibility string
	public     bool
	unlisted   bool
	protected  bool
	private    bool
}

func (v *visibilityFlags) register(f *pflag.FlagSet) {
	f.StringVar(&v.visibility, "visibility", "", "visibility: public|unlisted|protected|private")
	f.BoolVar(&v.public, "public", false, "shorthand for --visibility public")
	f.BoolVar(&v.unlisted, "unlisted", false, "shorthand for --visibility unlisted")
	f.BoolVar(&v.protected, "protected", false, "shorthand for --visibility protected")
	f.BoolVar(&v.private, "private", false, "shorthand for --visibility private")
}

// resolve returns the selected visibility ("" when no flag was used), or a
// usage error when flags conflict or --visibility has an invalid value.
func (v *visibilityFlags) resolve() (string, error) {
	selected := ""
	n := 0
	for _, s := range []struct {
		on   bool
		name string
	}{
		{v.public, "public"},
		{v.unlisted, "unlisted"},
		{v.protected, "protected"},
		{v.private, "private"},
	} {
		if s.on {
			selected = s.name
			n++
		}
	}
	if v.visibility != "" {
		if !validVisibility(v.visibility) {
			return "", usagef("invalid --visibility %q: want public|unlisted|protected|private", v.visibility)
		}
		selected = v.visibility
		n++
	}
	if n > 1 {
		return "", usagef("--public, --unlisted, --protected, --private, and --visibility are mutually exclusive")
	}
	return selected, nil
}

// authFlags is the shared object-auth flag group (--bearer-token /
// --generate-bearer-token / --basic). Auto-generation is an explicit boolean
// rather than an optional flag value, so --bearer-token parses its value with
// normal space-separated grammar like every other flag.
type authFlags struct {
	bearerToken    string
	generateBearer bool
	basic          string

	// genToken is set by entries() when a bearer token was auto-generated; the
	// server only stores its hash, so the caller must print it.
	genToken string
}

func (a *authFlags) register(f *pflag.FlagSet) {
	f.StringVar(&a.bearerToken, "bearer-token", "", "require this bearer token")
	f.BoolVar(&a.generateBearer, "generate-bearer-token", false, "generate a random bearer token and print it once")
	f.StringVar(&a.basic, "basic", "", "require basic auth <user:pass>")
}

// entries resolves the auth flags into access.auth entries, generating a
// bearer token when requested. Returns nil when no auth flag was used.
func (a *authFlags) entries() ([]map[string]any, error) {
	if a.bearerToken != "" && a.generateBearer {
		return nil, usagef("--bearer-token and --generate-bearer-token are mutually exclusive")
	}
	bearer := a.bearerToken
	if a.generateBearer {
		t, err := generateToken()
		if err != nil {
			return nil, err
		}
		a.genToken = t
		bearer = t
	}
	return buildAuthEntries(bearer, a.basic)
}

// buildAuthEntries assembles access.auth entries from cleartext bearer/basic
// credentials (the server hashes them; design/04 §3).
func buildAuthEntries(bearerToken, basic string) ([]map[string]any, error) {
	var auth []map[string]any
	if bearerToken != "" {
		auth = append(auth, map[string]any{"type": "bearer", "token": bearerToken})
	}
	if basic != "" {
		user, pass, ok := strings.Cut(basic, ":")
		if !ok {
			return nil, usagef("invalid --basic %q: want user:pass", basic)
		}
		auth = append(auth, map[string]any{"type": "basic", "username": user, "password": pass})
	}
	return auth, nil
}

// expiryFlags is the shared --expires/--expires-in pair; every command that
// accepts an expiry takes both forms (design/10 §2).
type expiryFlags struct {
	expires   string
	expiresIn string
}

func (e *expiryFlags) register(f *pflag.FlagSet) {
	f.StringVar(&e.expires, "expires", "", "expire at absolute RFC3339 timestamp")
	f.StringVar(&e.expiresIn, "expires-in", "", "expire after duration (e.g. 30d, 48h, 2w)")
}

// expiresAt resolves the pair into an RFC3339 timestamp, or "" when unset.
func (e *expiryFlags) expiresAt() (string, error) {
	return expiresAtFrom(e.expires, e.expiresIn)
}

// uploadMetaFlags collects the metadata-shaping flags shared by put and
// put-tree (design/10 §2, SPEC §20): visibility, object auth, response
// headers, indexability, and expiry.
type uploadMetaFlags struct {
	vis     visibilityFlags
	auth    authFlags
	expiry  expiryFlags
	headers []string
	noIndex bool
}

func (u *uploadMetaFlags) register(f *pflag.FlagSet) {
	u.vis.register(f)
	u.auth.register(f)
	u.expiry.register(f)
	f.StringArrayVar(&u.headers, "header", nil, "response header k=v (repeatable)")
	f.BoolVar(&u.noIndex, "no-index", false, "mark non-indexable")
}

// document assembles the metadata document the flags describe, or nil when no
// metadata flag was used (the server then applies pure defaults/inheritance).
func (u *uploadMetaFlags) document() (json.RawMessage, error) {
	visibility, err := u.vis.resolve()
	if err != nil {
		return nil, err
	}
	authEntries, err := u.auth.entries()
	if err != nil {
		return nil, err
	}
	hdrs, err := parseHeaders(u.headers)
	if err != nil {
		return nil, err
	}
	expiresAt, err := u.expiry.expiresAt()
	if err != nil {
		return nil, err
	}

	doc := map[string]any{}
	access := map[string]any{}
	if visibility != "" {
		access["visibility"] = visibility
	}
	if len(authEntries) > 0 {
		access["auth"] = authEntries
	}
	if len(access) > 0 {
		doc["access"] = access
	}
	if len(hdrs) > 0 {
		doc["content"] = map[string]any{"headers": hdrs}
	}
	if u.noIndex {
		doc["crawl"] = map[string]any{"indexable": false}
	}
	if expiresAt != "" {
		doc["expiry"] = map[string]any{"expires_at": expiresAt}
	}
	if len(doc) == 0 {
		return nil, nil
	}
	return json.Marshal(doc)
}

// printGeneratedToken shows an auto-generated bearer token once (the server
// only ever stores its hash).
func (a *app) printGeneratedToken(genToken string) {
	if genToken != "" {
		a.printf("bearer token (shown once, save it): %s\n", genToken)
	}
}
