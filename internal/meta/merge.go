package meta

import "net/http"

// Merge applies a patch document onto the receiver in place, using the same
// per-section semantics as inheritance resolution (design/03 §2): scalars
// override when set in the patch, content.headers map-merge (empty value
// removes), and access.auth replaces wholesale. Sections absent from the patch
// are left untouched. Header keys are matched in HTTP-canonical form so override
// and removal are case-insensitive (design/03 §6). Used by PATCH .../meta
// (own-metadata merge).
func (d *Document) Merge(patch *Document) {
	if patch == nil {
		return
	}
	if patch.Content != nil {
		if d.Content == nil {
			d.Content = &Content{}
		}
		if patch.Content.ContentType != nil {
			d.Content.ContentType = patch.Content.ContentType
		}
		if len(patch.Content.Headers) > 0 {
			// Canonicalize the base so case-variant patch keys hit the existing
			// entry; the merge then operates entirely in canonical form.
			d.Content.Headers = canonicalHeaders(d.Content.Headers)
			if d.Content.Headers == nil {
				d.Content.Headers = map[string]string{}
			}
			for k, v := range patch.Content.Headers {
				key := http.CanonicalHeaderKey(k)
				if v == "" {
					delete(d.Content.Headers, key)
				} else {
					d.Content.Headers[key] = v
				}
			}
		}
	}
	if patch.Access != nil {
		if d.Access == nil {
			d.Access = &Access{}
		}
		if patch.Access.Visibility != nil {
			d.Access.Visibility = patch.Access.Visibility
		}
		if patch.Access.DirectoryListing != nil {
			d.Access.DirectoryListing = patch.Access.DirectoryListing
		}
		if patch.Access.Auth != nil { // replace
			d.Access.Auth = patch.Access.Auth
		}
	}
	if patch.Crawl != nil {
		if d.Crawl == nil {
			d.Crawl = &Crawl{}
		}
		if patch.Crawl.Crawlable != nil {
			d.Crawl.Crawlable = patch.Crawl.Crawlable
		}
		if patch.Crawl.Indexable != nil {
			d.Crawl.Indexable = patch.Crawl.Indexable
		}
		if patch.Crawl.Robots != nil {
			d.Crawl.Robots = patch.Crawl.Robots
		}
	}
	if patch.Expiry != nil {
		if d.Expiry == nil {
			d.Expiry = &Expiry{}
		}
		if patch.Expiry.ExpiresAt != nil {
			d.Expiry.ExpiresAt = patch.Expiry.ExpiresAt
		}
		if patch.Expiry.DeleteAfterCreation != nil {
			d.Expiry.DeleteAfterCreation = patch.Expiry.DeleteAfterCreation
		}
		if patch.Expiry.DeleteAfterLastAccess != nil {
			d.Expiry.DeleteAfterLastAccess = patch.Expiry.DeleteAfterLastAccess
		}
	}
}
