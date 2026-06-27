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
	eff := &Effective{
		Visibility:       d.Visibility,
		DirectoryListing: d.DirectoryListing,
		Crawlable:        d.Crawlable,
		Indexable:        d.Indexable,
		Sandbox:          d.Sandbox,
		Headers:          map[string]string{},
	}
	for _, layer := range layers {
		if layer != nil {
			eff.apply(layer)
		}
	}
	if len(eff.Headers) == 0 {
		eff.Headers = nil
	}
	return eff
}

func (eff *Effective) apply(d *Document) {
	if d.Content != nil {
		if d.Content.ContentType != nil {
			eff.ContentType = *d.Content.ContentType
		}
		if d.Content.Sandbox != nil {
			eff.Sandbox = *d.Content.Sandbox
		}
		for k, v := range d.Content.Headers {
			key := http.CanonicalHeaderKey(k)
			if v == "" {
				delete(eff.Headers, key) // empty value removes an inherited header
			} else {
				eff.Headers[key] = v
			}
		}
	}

	if d.Access != nil {
		if d.Access.Visibility != nil {
			eff.Visibility = *d.Access.Visibility
		}
		if d.Access.DirectoryListing != nil {
			eff.DirectoryListing = *d.Access.DirectoryListing
		}
		if d.Access.Auth != nil { // replace (even with an empty list)
			eff.Auth = append([]AuthEntry(nil), (*d.Access.Auth)...)
		}
	}

	if d.Crawl != nil {
		if d.Crawl.Crawlable != nil {
			eff.Crawlable = *d.Crawl.Crawlable
		}
		if d.Crawl.Indexable != nil {
			eff.Indexable = *d.Crawl.Indexable
		}
		if d.Crawl.Robots != nil {
			eff.applyRobots(d.Crawl.Robots)
		}
	}
}

func (eff *Effective) applyRobots(r *Robots) {
	if len(r.UserAgents) > 0 {
		if eff.RobotsUserAgents == nil {
			eff.RobotsUserAgents = map[string]RobotRule{}
		}
		for ua, rule := range r.UserAgents {
			eff.RobotsUserAgents[ua] = rule
		}
	}
	if r.XRobotsTag != nil {
		eff.XRobotsTag = append([]string(nil), r.XRobotsTag...)
	}
}

// RequiresAuth reports whether the effective metadata configures any auth.
func (eff *Effective) RequiresAuth() bool {
	return len(eff.Auth) > 0
}
