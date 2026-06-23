package meta

import (
	"testing"
)

func boolp(b bool) *bool { return &b }

var testDefaults = Defaults{Visibility: VisibilityPublic, DirectoryListing: false, Crawlable: false, Indexable: false}

func TestResolve_DefaultsOnly(t *testing.T) {
	eff := Resolve(testDefaults)
	if eff.Visibility != VisibilityPublic || eff.DirectoryListing || eff.Crawlable || eff.Indexable {
		t.Errorf("defaults not applied: %+v", eff)
	}
	if eff.RequiresAuth() {
		t.Error("no auth should be required by default")
	}
}

func TestResolve_ScalarOverrideLastWins(t *testing.T) {
	collection := &Document{Access: &Access{Visibility: strp(VisibilityUnlisted)}}
	file := &Document{Access: &Access{Visibility: strp(VisibilityProtected), DirectoryListing: boolp(true)}}
	eff := Resolve(testDefaults, collection, file)
	if eff.Visibility != VisibilityProtected {
		t.Errorf("visibility = %q, want protected (last wins)", eff.Visibility)
	}
	if !eff.DirectoryListing {
		t.Error("directory_listing not overridden to true")
	}
}

func TestResolve_HeaderMapMergeAndDelete(t *testing.T) {
	collection := &Document{Content: &Content{Headers: map[string]string{
		"Cache-Control": "max-age=60",
		"X-Custom":      "keep",
	}}}
	file := &Document{Content: &Content{Headers: map[string]string{
		"cache-control": "no-store", // overrides (case-insensitive)
		"X-Remove":      "",         // (nothing to remove; no-op)
	}}}
	folder := &Document{Content: &Content{Headers: map[string]string{
		"X-Custom": "", // removes the inherited header
	}}}
	eff := Resolve(testDefaults, collection, folder, file)
	if eff.Headers["Cache-Control"] != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", eff.Headers["Cache-Control"])
	}
	if _, ok := eff.Headers["X-Custom"]; ok {
		t.Error("X-Custom should have been removed by empty-value override")
	}
}

func TestResolve_AuthReplaceNotAppend(t *testing.T) {
	parentAuth := []AuthEntry{{Type: AuthBearer, TokenHash: "parent"}}
	childAuth := []AuthEntry{{Type: AuthBasic, Username: "u", PasswordHash: "h"}}
	collection := &Document{Access: &Access{Auth: &parentAuth}}
	file := &Document{Access: &Access{Auth: &childAuth}}

	eff := Resolve(testDefaults, collection, file)
	if len(eff.Auth) != 1 || eff.Auth[0].Type != AuthBasic {
		t.Errorf("auth not replaced: %+v", eff.Auth)
	}
}

func TestResolve_AuthEmptyListClearsInherited(t *testing.T) {
	parentAuth := []AuthEntry{{Type: AuthBearer, TokenHash: "x"}}
	empty := []AuthEntry{}
	collection := &Document{Access: &Access{Auth: &parentAuth}}
	file := &Document{Access: &Access{Auth: &empty}}

	eff := Resolve(testDefaults, collection, file)
	if eff.RequiresAuth() {
		t.Errorf("explicit empty auth should clear inherited auth: %+v", eff.Auth)
	}
}

func TestResolve_AuthUnsetInherits(t *testing.T) {
	parentAuth := []AuthEntry{{Type: AuthBearer, TokenHash: "x"}}
	collection := &Document{Access: &Access{Auth: &parentAuth}}
	file := &Document{Access: &Access{Visibility: strp(VisibilityProtected)}} // no auth set

	eff := Resolve(testDefaults, collection, file)
	if len(eff.Auth) != 1 || eff.Auth[0].TokenHash != "x" {
		t.Errorf("unset auth should inherit parent: %+v", eff.Auth)
	}
}

func TestResolve_CrawlAndRobots(t *testing.T) {
	collection := &Document{Crawl: &Crawl{
		Crawlable: boolp(true),
		Robots: &Robots{
			UserAgents: map[string]RobotRule{"*": {Disallow: []string{"/private/"}}},
			XRobotsTag: []string{"noindex"},
		},
	}}
	file := &Document{Crawl: &Crawl{
		Indexable: boolp(true),
		Robots:    &Robots{UserAgents: map[string]RobotRule{"Googlebot": {Allow: []string{"/"}}}},
	}}
	eff := Resolve(testDefaults, collection, file)
	if !eff.Crawlable || !eff.Indexable {
		t.Errorf("crawl flags = crawlable:%v indexable:%v", eff.Crawlable, eff.Indexable)
	}
	if len(eff.RobotsUserAgents) != 2 {
		t.Errorf("robots user_agents not map-merged: %+v", eff.RobotsUserAgents)
	}
	if len(eff.XRobotsTag) != 1 || eff.XRobotsTag[0] != "noindex" {
		t.Errorf("x_robots_tag = %v", eff.XRobotsTag)
	}
}

func TestResolve_NilLayersSkipped(t *testing.T) {
	file := &Document{Access: &Access{Visibility: strp(VisibilityPrivate)}}
	eff := Resolve(testDefaults, nil, nil, file, nil)
	if eff.Visibility != VisibilityPrivate {
		t.Errorf("visibility = %q, want private", eff.Visibility)
	}
}
