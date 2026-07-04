package meta

import "testing"

func TestResolveTraced_Sources(t *testing.T) {
	defaults := Defaults{Visibility: "public", Sandbox: true}
	collection := &Document{
		Access: &Access{Visibility: strp("protected")},
		Content: &Content{
			Headers: map[string]string{"cache-control": "no-store", "x-a": "1"},
		},
	}
	folder := &Document{
		Access:  &Access{Visibility: strp("unlisted")},
		Content: &Content{Headers: map[string]string{"X-A": ""}}, // removes inherited x-a
		Crawl:   &Crawl{Crawlable: boolp(true)},
	}
	file := &Document{
		Content: &Content{ContentType: strp("text/html")},
	}

	eff, sources := ResolveTraced(
		defaults,
		Layer{Label: "collection", Doc: collection},
		Layer{Label: "folder docs", Doc: folder},
		Layer{Label: "file docs/a.html", Doc: file},
	)

	// The traced result must match plain Resolve.
	plain := Resolve(defaults, collection, folder, file)
	if eff.Visibility != plain.Visibility || eff.ContentType != plain.ContentType ||
		eff.Crawlable != plain.Crawlable || len(eff.Headers) != len(plain.Headers) {
		t.Fatalf("traced effective %+v != plain %+v", eff, plain)
	}

	want := map[string]string{
		"visibility":            "folder docs", // folder overrode the collection
		"content_type":          "file docs/a.html",
		"crawlable":             "folder docs",
		"indexable":             "defaults",
		"sandbox":               "defaults",
		"directory_listing":     "defaults",
		"headers.Cache-Control": "collection",
	}
	for prop, label := range want {
		if got := sources[prop]; got != label {
			t.Errorf("sources[%q] = %q, want %q", prop, got, label)
		}
	}
	// The header removed by the folder layer must not report a source.
	if src, ok := sources["headers.X-A"]; ok {
		t.Errorf("sources[headers.X-A] = %q, want absent (removed)", src)
	}
	if _, ok := eff.Headers["X-A"]; ok {
		t.Errorf("X-A still in effective headers: %+v", eff.Headers)
	}
}

func TestResolveTraced_AuthReplace(t *testing.T) {
	auth := []AuthEntry{{Type: "bearer"}}
	collection := &Document{Access: &Access{Auth: &auth}}
	empty := []AuthEntry{}
	file := &Document{Access: &Access{Auth: &empty}}

	_, sources := ResolveTraced(
		Defaults{Visibility: "public"},
		Layer{Label: "collection", Doc: collection},
		Layer{Label: "file a.txt", Doc: file},
	)
	if sources["auth"] != "file a.txt" {
		t.Errorf("sources[auth] = %q, want file a.txt (explicit empty replaces)", sources["auth"])
	}
}
