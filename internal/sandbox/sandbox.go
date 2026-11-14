// Package sandbox computes how an object is served with respect to the CSP
// sandbox, for display: listings, metadata views and write responses
// (artifactsd-7tl.9). Serving itself applies the rule in internal/serve.
//
// A file is served without the sandbox only when its effective
// content.sandbox is false AND its current bytes are trusted (written or
// approved by an admin, artifactsd-7tl.2). Share-level overrides apply only
// to requests through that share and are not reflected here.
package sandbox

import (
	"strings"

	"github.com/jdelic/artifactsd/internal/meta"
	"github.com/jdelic/artifactsd/internal/store"
)

// State is an object's sandbox state.
type State string

const (
	// Sandboxed: served inside the CSP sandbox (the default).
	Sandboxed State = "sandboxed"
	// Unsandboxed: an admin turned the sandbox off and the content is
	// approved; served with its real origin.
	Unsandboxed State = "unsandboxed"
	// AwaitingApproval: the sandbox is turned off for this location, but the
	// content changed since an admin approved it (a non-admin wrote it), so
	// it is served sandboxed until an admin re-approves it.
	AwaitingApproval State = "awaiting_approval"
)

// AwaitingApprovalNote explains AwaitingApproval to users.
const AwaitingApprovalNote = "the sandbox is turned off here, but this content was written by a non-admin " +
	"since an admin approved it, so it is served sandboxed until an admin re-approves it " +
	"(by setting content.sandbox=false again)"

// StateOf returns the state of an object whose effective content.sandbox is
// effSandbox. trusted applies to files only (folders and collections pass true).
func StateOf(effSandbox, isFile, trusted bool) State {
	switch {
	case effSandbox:
		return Sandboxed
	case isFile && !trusted:
		return AwaitingApproval
	default:
		return Unsandboxed
	}
}

// NodeStates computes the state of every node of one collection in a single
// pass: defaultSandbox is the server default, collMeta the collection's own
// metadata, and nodes all of the collection's nodes (any order). Unparseable
// metadata is treated as setting nothing.
func NodeStates(defaultSandbox bool, collMeta []byte, nodes []*store.Node) map[string]State {
	own := make(map[string]*bool, len(nodes))
	for _, n := range nodes {
		own[n.Path] = ownSandbox(n.Metadata)
	}
	base := defaultSandbox
	if v := ownSandbox(collMeta); v != nil {
		base = *v
	}
	out := make(map[string]State, len(nodes))
	for _, n := range nodes {
		eff := base
		prefix := ""
		for _, seg := range strings.Split(n.Path, "/") {
			if prefix == "" {
				prefix = seg
			} else {
				prefix += "/" + seg
			}
			if v := own[prefix]; v != nil {
				eff = *v
			}
		}
		isFile := n.Kind == store.KindFile
		out[n.Path] = StateOf(eff, isFile, !isFile || n.ContentTrusted)
	}
	return out
}

// CollectionState is the state of a collection itself (its own setting over
// the server default).
func CollectionState(defaultSandbox bool, collMeta []byte) State {
	eff := defaultSandbox
	if v := ownSandbox(collMeta); v != nil {
		eff = *v
	}
	return StateOf(eff, false, true)
}

func ownSandbox(raw []byte) *bool {
	d, err := meta.FromJSON(raw)
	if err != nil || d == nil || d.Content == nil {
		return nil
	}
	return d.Content.Sandbox
}
