package meta

import "net/http"

// Effective is the resolved metadata for a served object after merging the
// inheritance chain (design/03 §2). Expiry is resolved separately (earliest in
// tree always wins) by ResolveExpiry.
type Effective struct {
	ContentType      string
	Headers          map[string]string
	Sandbox          bool
	Visibility       string
	DirectoryListing bool
	Auth             []AuthEntry
	Crawlable        bool
	Indexable        bool
	XRobotsTag       []string
	RobotsUserAgents map[string]RobotRule
}

// Defaults is the server-defaults base layer (from config flags/env, design/12
// §3) that resolution starts from.
type Defaults struct {
	Visibility       string
	DirectoryListing bool
	Crawlable        bool
	Indexable        bool
	Sandbox          bool
}

// Resolve merges the layers in increasing precedence onto the server defaults
// (design/03 §2): collection → folders (outermost→innermost) → file → share.
// Nil layers are skipped. Section merge rules:
//   - content.content_type: scalar override (last wins)
//   - content.sandbox: scalar override (last wins)
//   - content.headers: map-merge by canonical name; "" value removes inherited
//   - access.visibility / directory_listing: scalar override
//   - access.auth: replace (any level that sets auth replaces the list; an
//     explicit empty list means "no auth")
//   - crawl.crawlable / indexable: scalar override
//   - crawl.robots.user_agents: map-merge; x_robots_tag: replace
func Resolve(d Defaults, layers ...*Document) *Effective {
	eff, _ := resolve(d, nil, layers...)
	return eff
}

// Layer pairs a metadata document with the human-readable label of where it
// came from ("collection", "folder docs", "file docs/a.txt"), for provenance
// tracking (ResolveTraced).
type Layer struct {
	Label string
	Doc   *Document
}

// ResolveTraced is Resolve plus provenance: alongside the effective metadata
// it returns a map from each effective property to the label of the layer
// that determined it. Scalar defaults are labeled "defaults"; header keys are
// tracked per header as "headers.<Canonical-Name>", robots user-agents as
// "robots.<ua>". A property absent from the map was never set by any layer.
func ResolveTraced(d Defaults, layers ...Layer) (*Effective, map[string]string) {
	return resolve(d, layers, nil)
}

// resolve is the single merge implementation behind Resolve and
// ResolveTraced: exactly one of traced/plain is used. When traced is non-nil,
// every field application records its layer label in the returned sources map.
func resolve(d Defaults, traced []Layer, plain ...*Document) (*Effective, map[string]string) {
	eff := &Effective{
		Visibility:       d.Visibility,
		DirectoryListing: d.DirectoryListing,
		Crawlable:        d.Crawlable,
		Indexable:        d.Indexable,
		Sandbox:          d.Sandbox,
		Headers:          map[string]string{},
	}
	var sources map[string]string
	if traced != nil {
		sources = map[string]string{
			"visibility": "defaults", "directory_listing": "defaults",
			"crawlable": "defaults", "indexable": "defaults", "sandbox": "defaults",
		}
		for _, l := range traced {
			if l.Doc != nil {
				eff.apply(l.Doc, &tracer{label: l.Label, sources: sources})
			}
		}
	} else {
		for _, doc := range plain {
			if doc != nil {
				eff.apply(doc, nil)
			}
		}
	}
	if len(eff.Headers) == 0 {
		eff.Headers = nil
	}
	return eff, sources
}

// tracer records which layer set which effective property. A nil *tracer is
// a no-op, so the merge logic below stays single-sourced.
type tracer struct {
	label   string
	sources map[string]string
}

func (t *tracer) set(prop string) {
	if t != nil {
		t.sources[prop] = t.label
	}
}

func (t *tracer) unset(prop string) {
	if t != nil {
		delete(t.sources, prop)
	}
}

func (eff *Effective) apply(d *Document, t *tracer) {
	if d.Content != nil {
		if d.Content.ContentType != nil {
			eff.ContentType = *d.Content.ContentType
			t.set("content_type")
		}
		if d.Content.Sandbox != nil {
			eff.Sandbox = *d.Content.Sandbox
			t.set("sandbox")
		}
		for k, v := range d.Content.Headers {
			key := http.CanonicalHeaderKey(k)
			if v == "" {
				delete(eff.Headers, key) // empty value removes an inherited header
				t.unset("headers." + key)
			} else {
				eff.Headers[key] = v
				t.set("headers." + key)
			}
		}
	}

	if d.Access != nil {
		if d.Access.Visibility != nil {
			eff.Visibility = *d.Access.Visibility
			t.set("visibility")
		}
		if d.Access.DirectoryListing != nil {
			eff.DirectoryListing = *d.Access.DirectoryListing
			t.set("directory_listing")
		}
		if d.Access.Auth != nil { // replace (even with an empty list)
			eff.Auth = append([]AuthEntry(nil), (*d.Access.Auth)...)
			t.set("auth")
		}
	}

	if d.Crawl != nil {
		if d.Crawl.Crawlable != nil {
			eff.Crawlable = *d.Crawl.Crawlable
			t.set("crawlable")
		}
		if d.Crawl.Indexable != nil {
			eff.Indexable = *d.Crawl.Indexable
			t.set("indexable")
		}
		if d.Crawl.Robots != nil {
			eff.applyRobots(d.Crawl.Robots, t)
		}
	}
}

func (eff *Effective) applyRobots(r *Robots, t *tracer) {
	if len(r.UserAgents) > 0 {
		if eff.RobotsUserAgents == nil {
			eff.RobotsUserAgents = map[string]RobotRule{}
		}
		for ua, rule := range r.UserAgents {
			eff.RobotsUserAgents[ua] = rule
			t.set("robots." + ua)
		}
	}
	if r.XRobotsTag != nil {
		eff.XRobotsTag = append([]string(nil), r.XRobotsTag...)
		t.set("x_robots_tag")
	}
}

// RequiresAuth reports whether the effective metadata configures any auth.
func (eff *Effective) RequiresAuth() bool {
	return len(eff.Auth) > 0
}
