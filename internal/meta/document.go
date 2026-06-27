// Package meta defines the artifactsd metadata document and the engine that
// resolves effective metadata across the inheritance chain (design/03). The
// same document shape attaches to collections, folders, files, and shares; all
// sections are optional and absent fields inherit.
//
// Secrets (bearer token, basic password) are accepted only on write, hashed
// immediately, and never persisted or returned in cleartext (design/06 §2).
package meta

import (
	"bytes"
	"encoding/json"
	"fmt"

	"gopkg.in/yaml.v3"
)

// Visibility modes (design/03 §3).
const (
	VisibilityPublic    = "public"
	VisibilityUnlisted  = "unlisted"
	VisibilityProtected = "protected"
	VisibilityPrivate   = "private"
)

// Auth entry types.
const (
	AuthBearer = "bearer"
	AuthBasic  = "basic"
	AuthOIDC   = "oidc"
)

// Document is a metadata document. Optional sections use pointers so "unset"
// (inherit) is distinguishable from "set to zero".
type Document struct {
	Content *Content `json:"content,omitempty" yaml:"content,omitempty"`
	Access  *Access  `json:"access,omitempty" yaml:"access,omitempty"`
	Crawl   *Crawl   `json:"crawl,omitempty" yaml:"crawl,omitempty"`
	Expiry  *Expiry  `json:"expiry,omitempty" yaml:"expiry,omitempty"`
}

// Content holds content-type and custom response headers.
type Content struct {
	ContentType *string           `json:"content_type,omitempty" yaml:"content_type,omitempty"`
	Headers     map[string]string `json:"headers,omitempty" yaml:"headers,omitempty"`
	// Sandbox overrides the server default for whether this object is served
	// with a CSP sandbox (design/14 §7, artifactsd-c63). Pointer so "unset"
	// (inherit the default) is distinct from an explicit false (trusted content
	// served on its real origin) or true. Set false only for content you trust:
	// it regains same-origin powers on the shared admin/artifact origin.
	Sandbox *bool `json:"sandbox,omitempty" yaml:"sandbox,omitempty"`
}

// Access holds visibility, directory listing, and the auth requirement list.
// Auth is a pointer-to-slice so that an explicit empty list ("no auth") is
// distinguishable from "unset" (inherit) — design/03 §2.
type Access struct {
	Visibility       *string      `json:"visibility,omitempty" yaml:"visibility,omitempty"`
	DirectoryListing *bool        `json:"directory_listing,omitempty" yaml:"directory_listing,omitempty"`
	Auth             *[]AuthEntry `json:"auth,omitempty" yaml:"auth,omitempty"`
}

// AuthEntry is one accepted credential (evaluated any-of). Cleartext Token and
// Password are write-only; they are hashed into TokenHash/PasswordHash and then
// cleared. HasToken/HasPassword are read-only presence flags for API output.
type AuthEntry struct {
	Type string `json:"type" yaml:"type"`

	// bearer
	Token     string `json:"token,omitempty" yaml:"token,omitempty"`
	TokenHash string `json:"token_hash,omitempty" yaml:"token_hash,omitempty"`
	HasToken  bool   `json:"has_token,omitempty" yaml:"-"`

	// basic
	Username     string `json:"username,omitempty" yaml:"username,omitempty"`
	Password     string `json:"password,omitempty" yaml:"password,omitempty"`
	PasswordHash string `json:"password_hash,omitempty" yaml:"password_hash,omitempty"`
	HasPassword  bool   `json:"has_password,omitempty" yaml:"-"`

	// oidc
	Issuer          string   `json:"issuer,omitempty" yaml:"issuer,omitempty"`
	Audience        string   `json:"audience,omitempty" yaml:"audience,omitempty"`
	AllowedSubjects []string `json:"allowed_subjects,omitempty" yaml:"allowed_subjects,omitempty"`
	AllowedGroups   []string `json:"allowed_groups,omitempty" yaml:"allowed_groups,omitempty"`
	GroupClaim      string   `json:"group_claim,omitempty" yaml:"group_claim,omitempty"`
	// InsecureSkipAudienceCheck disables ID-token audience (aud) verification.
	// Audience is required on OIDC entries (design/06 §1.3, design/14 §2.3); this
	// flag is the explicit, loud opt-out for the rare issuer that mints tokens
	// without a usable audience. It must be set deliberately — an empty audience
	// no longer silently skips the check (artifactsd-bhq).
	InsecureSkipAudienceCheck bool `json:"insecure_skip_audience_check,omitempty" yaml:"insecure_skip_audience_check,omitempty"`
	AnyAuthenticated          bool `json:"any_authenticated,omitempty" yaml:"any_authenticated,omitempty"`
}

// Crawl holds crawl/index directives.
type Crawl struct {
	Crawlable *bool   `json:"crawlable,omitempty" yaml:"crawlable,omitempty"`
	Indexable *bool   `json:"indexable,omitempty" yaml:"indexable,omitempty"`
	Robots    *Robots `json:"robots,omitempty" yaml:"robots,omitempty"`
}

// Robots holds per-user-agent rules and X-Robots-Tag values.
type Robots struct {
	UserAgents map[string]RobotRule `json:"user_agents,omitempty" yaml:"user_agents,omitempty"`
	XRobotsTag []string             `json:"x_robots_tag,omitempty" yaml:"x_robots_tag,omitempty"`
}

// RobotRule is one user-agent's allow/disallow lists.
type RobotRule struct {
	Allow    []string `json:"allow,omitempty" yaml:"allow,omitempty"`
	Disallow []string `json:"disallow,omitempty" yaml:"disallow,omitempty"`
}

// Expiry holds the three expiry directives (resolved in design/03 §4).
type Expiry struct {
	ExpiresAt             *string `json:"expires_at,omitempty" yaml:"expires_at,omitempty"`
	DeleteAfterLastAccess *string `json:"delete_after_last_access,omitempty" yaml:"delete_after_last_access,omitempty"`
	DeleteAfterCreation   *string `json:"delete_after_creation,omitempty" yaml:"delete_after_creation,omitempty"`
}

// FromJSON parses a metadata document from JSON, rejecting unknown fields.
func FromJSON(data []byte) (*Document, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return &Document{}, nil
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var d Document
	if err := dec.Decode(&d); err != nil {
		return nil, fmt.Errorf("meta: parsing JSON: %w", err)
	}
	d.normalize()
	return &d, nil
}

// FromYAML parses a metadata document from YAML (CLI input, SPEC §6).
func FromYAML(data []byte) (*Document, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return &Document{}, nil
	}
	var d Document
	if err := yaml.Unmarshal(data, &d); err != nil {
		return nil, fmt.Errorf("meta: parsing YAML: %w", err)
	}
	d.normalize()
	return &d, nil
}

// normalize canonicalizes the document into its stored form. Currently this
// canonicalizes content.headers keys so that stored documents, merges, and
// resolution all agree on header casing (design/03 §6); applied on every parse
// so both freshly-written and already-stored documents are normalized.
func (d *Document) normalize() {
	if d.Content != nil && len(d.Content.Headers) > 0 {
		d.Content.Headers = canonicalHeaders(d.Content.Headers)
	}
}

// JSON serializes the document to compact JSON (the storage form).
func (d *Document) JSON() ([]byte, error) {
	return json.Marshal(d)
}

// Clone returns a deep copy of the document.
func (d *Document) Clone() *Document {
	if d == nil {
		return nil
	}
	data, _ := json.Marshal(d)
	var c Document
	_ = json.Unmarshal(data, &c)
	// has_* flags are not serialized in storage form; copy them explicitly.
	if d.Access != nil && d.Access.Auth != nil && c.Access != nil && c.Access.Auth != nil {
		for i := range *d.Access.Auth {
			(*c.Access.Auth)[i].HasToken = (*d.Access.Auth)[i].HasToken
			(*c.Access.Auth)[i].HasPassword = (*d.Access.Auth)[i].HasPassword
		}
	}
	return &c
}
