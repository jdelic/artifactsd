package sandbox

import (
	"testing"

	"github.com/jdelic/artifactsd/internal/store"
)

func TestNodeStates(t *testing.T) {
	file := func(p, md string, trusted bool) *store.Node {
		return &store.Node{Path: p, Kind: store.KindFile, Metadata: []byte(md), ContentTrusted: trusted}
	}
	folder := func(p, md string) *store.Node {
		return &store.Node{Path: p, Kind: store.KindFolder, Metadata: []byte(md)}
	}
	nodes := []*store.Node{
		folder("spa", `{"content":{"sandbox":false}}`),
		file("spa/ok.html", `{}`, true),
		file("spa/new.html", `{}`, false),
		folder("spa/locked", `{"content":{"sandbox":true}}`),
		file("spa/locked/x.html", `{}`, true),
		file("plain.html", `{}`, true),
		file("own.html", `{"content":{"sandbox":false}}`, true),
		folder("spa-other", `{}`),
		file("spa-other/y.html", `{}`, false),
	}
	got := NodeStates(true, []byte(`{}`), nodes)
	want := map[string]State{
		"spa": Unsandboxed, "spa/ok.html": Unsandboxed, "spa/new.html": AwaitingApproval,
		"spa/locked": Sandboxed, "spa/locked/x.html": Sandboxed, "plain.html": Sandboxed,
		"own.html": Unsandboxed, "spa-other": Sandboxed, "spa-other/y.html": Sandboxed,
	}
	for p, w := range want {
		if got[p] != w {
			t.Errorf("%s = %s, want %s", p, got[p], w)
		}
	}
	// A collection-level opt-out applies everywhere not overridden.
	got = NodeStates(true, []byte(`{"content":{"sandbox":false}}`), nodes)
	if got["plain.html"] != Unsandboxed || got["spa-other/y.html"] != AwaitingApproval || got["spa/locked/x.html"] != Sandboxed {
		t.Errorf("collection opt-out: %v", got)
	}
	if CollectionState(true, []byte(`{"content":{"sandbox":false}}`)) != Unsandboxed || CollectionState(true, nil) != Sandboxed {
		t.Error("CollectionState")
	}
}
