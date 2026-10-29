package main

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jdelic/artifactsd/pkg/apiclient"
)

// metaTargetKind selects which meta endpoints a target routes to.
type metaTargetKind int

const (
	targetCollection metaTargetKind = iota // bare collection name
	targetFolder                           // trailing slash forces the folder endpoints
	targetNode                             // file (or an already-materialized folder)
)

// splitMetaTarget parses a meta command target (artifactsd-2db):
//
//	"demo"            → the collection's own metadata
//	"demo/docs/"      → the docs folder (trailing slash; materializes on set)
//	"demo/docs/a.txt" → the file (or existing folder) node at that path
func splitMetaTarget(arg string) (collection, path string, kind metaTargetKind, err error) {
	collection, path, found := strings.Cut(arg, "/")
	if collection == "" {
		return "", "", 0, usagef("target %q must be <collection>[/<path>]", arg)
	}
	if !found || path == "" {
		return collection, "", targetCollection, nil
	}
	if trimmed := strings.TrimSuffix(path, "/"); trimmed != path {
		if trimmed == "" {
			return collection, "", targetCollection, nil
		}
		return collection, trimmed, targetFolder, nil
	}
	return collection, path, targetNode, nil
}

// newMetaCmd builds `artifactctl meta ...` (design/10 §2).
func (a *app) newMetaCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "meta",
		Short: "Read, update, or explain collection, folder, and file metadata",
	}
	cmd.AddCommand(a.newMetaGetCmd(), a.newMetaSetCmd(), a.newMetaExplainCmd())
	return cmd
}

// fetchMeta resolves a meta target and fetches its {own, effective, chain,
// sources} view from the matching endpoint.
func (a *app) fetchMeta(cmd *cobra.Command, target string) (*apiclient.FileMeta, error) {
	cli, err := a.client()
	if err != nil {
		return nil, err
	}
	collection, path, kind, err := splitMetaTarget(target)
	if err != nil {
		return nil, err
	}
	switch kind {
	case targetCollection:
		return cli.GetCollectionMeta(cmd.Context(), collection)
	case targetFolder:
		return cli.GetFolderMeta(cmd.Context(), collection, path)
	default:
		return cli.GetFileMeta(cmd.Context(), collection, path)
	}
}

func (a *app) newMetaGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <collection>[/<path>]",
		Short: "Show an object's own and effective metadata",
		Long: "Show own and effective metadata for a collection (bare name), a folder\n" +
			"(trailing slash), or a file.",
		Args: exactArgs(1, "<collection>[/<path>]"),
		RunE: func(cmd *cobra.Command, args []string) error {
			meta, err := a.fetchMeta(cmd, args[0])
			if err != nil {
				return err
			}
			if a.jsonOutput() {
				return a.printJSON(meta)
			}
			a.printf("path:      %s\n", meta.Path)
			a.printf("own:       %s\n", meta.Own)
			a.printf("effective: %s\n", meta.Effective)
			return nil
		},
	}
}

// explainOrder fixes a readable property order for `meta explain`; header and
// robots keys (sorted) are appended after their section anchor.
var explainOrder = []string{
	"visibility", "directory_listing", "auth", "content_type", "sandbox",
	"crawlable", "indexable", "expires_at", "x_robots_tag",
}

func (a *app) newMetaExplainCmd() *cobra.Command {
	var fullChain bool
	cmd := &cobra.Command{
		Use:   "explain <collection>[/<path>]",
		Short: "Show effective metadata with the layer each property came from",
		Long: "Show the resolved effective metadata for a collection (bare name), folder\n" +
			"(trailing slash), or file, annotating every property with the inheritance\n" +
			"layer that determined it. --full-chain additionally prints each layer's own\n" +
			"metadata, lowest precedence first.",
		Args: exactArgs(1, "<collection>[/<path>]"),
		RunE: func(cmd *cobra.Command, args []string) error {
			meta, err := a.fetchMeta(cmd, args[0])
			if err != nil {
				return err
			}
			if a.jsonOutput() {
				return a.printJSON(meta)
			}
			if fullChain {
				a.printf("chain (lowest precedence first):\n")
				for _, e := range meta.Chain {
					name := e.Scope
					if e.Path != "" {
						name += " " + e.Path
					}
					a.printf("  %-11s %s\n", name+":", compactJSON(e.Meta))
				}
				a.printf("\n")
			}
			a.printf("effective:\n")
			var eff map[string]json.RawMessage
			if err := json.Unmarshal(meta.Effective, &eff); err != nil {
				return err
			}
			for _, prop := range explainProps(eff, meta.Sources) {
				a.printf("  %-24s %-22s (from %s)\n", prop.name+":", prop.value, prop.source)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&fullChain, "full-chain", false, "also print every inheritance layer's own metadata")
	return cmd
}

// explainProp is one annotated line of `meta explain` output.
type explainProp struct {
	name, value, source string
}

// explainProps flattens the effective view into ordered, source-annotated
// rows: scalar properties in explainOrder order, then per-key headers and
// robots rules (which carry per-key sources).
func explainProps(eff map[string]json.RawMessage, sources map[string]string) []explainProp {
	src := func(key string) string {
		if s, ok := sources[key]; ok {
			return s
		}
		return "defaults"
	}
	var props []explainProp
	for _, name := range explainOrder {
		raw, ok := eff[name]
		if !ok || string(raw) == "null" {
			continue
		}
		props = append(props, explainProp{name: name, value: compactJSON(raw), source: src(name)})
	}
	for _, section := range []string{"headers", "robots"} {
		raw, ok := eff[section]
		if !ok {
			continue
		}
		var m map[string]json.RawMessage
		if json.Unmarshal(raw, &m) != nil {
			continue
		}
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			full := section + "." + k
			props = append(props, explainProp{name: full, value: compactJSON(m[k]), source: src(full)})
		}
	}
	return props
}

// compactJSON renders raw JSON on one line for the text views.
func compactJSON(raw json.RawMessage) string {
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return string(raw)
	}
	return buf.String()
}

func (a *app) newMetaSetCmd() *cobra.Command {
	var (
		vis         visibilityFlags
		auth        authFlags
		clearAuth   bool
		expiry      expiryFlags
		headers     []string
		list        bool
		noList      bool
		crawlable   bool
		noCrawlable bool
		index       bool
		noIndex     bool
	)
	cmd := &cobra.Command{
		Use:   "set <collection>[/<path>]",
		Short: "Update an object's metadata",
		Long: "Merge a metadata patch onto a collection (bare name), a folder (trailing\n" +
			"slash; the folder is materialized if it does not exist yet), or a file.",
		Args: exactArgs(1, "<collection>[/<path>]"),
		RunE: func(cmd *cobra.Command, args []string) error {
			cli, err := a.client()
			if err != nil {
				return err
			}
			collection, path, kind, err := splitMetaTarget(args[0])
			if err != nil {
				return err
			}
			visibility, err := vis.resolve()
			if err != nil {
				return err
			}
			expiresAt, err := expiry.expiresAt()
			if err != nil {
				return err
			}
			authEntries, err := auth.entries()
			if err != nil {
				return err
			}
			if clearAuth {
				if authEntries != nil {
					return usagef("--clear-auth cannot be combined with --bearer-token, --generate-bearer-token, or --basic")
				}
				authEntries = []map[string]any{}
			}
			patch, err := buildFileMetaPatch(visibility, expiresAt, headers, authEntries,
				metaSetBools{list: list, noList: noList, crawlable: crawlable, noCrawlable: noCrawlable, index: index, noIndex: noIndex})
			if err != nil {
				return err
			}
			a.printGeneratedToken(auth.genToken)
			switch kind {
			case targetCollection:
				col, err := cli.PatchCollectionMeta(cmd.Context(), collection, patch)
				if err != nil {
					return err
				}
				if a.jsonOutput() {
					return a.printJSON(col)
				}
				a.printf("updated metadata for %s\n", col.Name)
				return nil
			case targetFolder:
				meta, err := cli.PatchFolderMeta(cmd.Context(), collection, path, patch)
				if err != nil {
					return err
				}
				if a.jsonOutput() {
					return a.printJSON(meta)
				}
				a.printf("updated metadata for %s/\n", meta.Path)
				return nil
			default:
				meta, err := cli.PatchFileMeta(cmd.Context(), collection, path, patch)
				if err != nil {
					return err
				}
				if a.jsonOutput() {
					return a.printJSON(meta)
				}
				a.printf("updated metadata for %s\n", meta.Path)
				return nil
			}
		},
	}
	f := cmd.Flags()
	vis.register(f)
	auth.register(f)
	f.BoolVar(&clearAuth, "clear-auth", false, "remove this object's own auth entries (set an empty list, overriding inherited auth)")
	expiry.register(f)
	f.StringArrayVar(&headers, "header", nil, "response header k=v (repeatable)")
	f.BoolVar(&list, "list", false, "enable directory listing on folder URLs")
	f.BoolVar(&noList, "no-list", false, "disable directory listing")
	f.BoolVar(&crawlable, "crawlable", false, "include in the crawl tree (artifactctl crawl)")
	f.BoolVar(&noCrawlable, "no-crawlable", false, "exclude from the crawl tree")
	f.BoolVar(&index, "index", false, "allow search indexing (robots.txt, sitemap)")
	f.BoolVar(&noIndex, "no-index", false, "send X-Robots-Tag: noindex")
	return cmd
}

// metaSetBools carries `meta set`'s paired boolean flags.
type metaSetBools struct {
	list, noList, crawlable, noCrawlable, index, noIndex bool
}

// buildFileMetaPatch assembles a metadata document patch from the `meta set`
// flags (design/03 §2 for the document shape); visibility and expiresAt arrive
// already resolved. authEntries nil leaves auth untouched; a non-nil empty
// slice replaces it with "no auth".
func buildFileMetaPatch(visibility, expiresAt string, headers []string, authEntries []map[string]any, b metaSetBools) (json.RawMessage, error) {
	if b.list && b.noList {
		return nil, usagef("--list and --no-list are mutually exclusive")
	}
	crawl, err := buildCrawlPatch(b.crawlable, b.noCrawlable, b.index, b.noIndex)
	if err != nil {
		return nil, err
	}
	hdrMap, err := parseHeaders(headers)
	if err != nil {
		return nil, err
	}
	access := map[string]any{}
	if visibility != "" {
		access["visibility"] = visibility
	}
	if b.list {
		access["directory_listing"] = true
	}
	if b.noList {
		access["directory_listing"] = false
	}
	if authEntries != nil {
		access["auth"] = authEntries
	}
	doc := map[string]any{}
	if len(access) > 0 {
		doc["access"] = access
	}
	if len(hdrMap) > 0 {
		doc["content"] = map[string]any{"headers": hdrMap}
	}
	if len(crawl) > 0 {
		doc["crawl"] = crawl
	}
	if expiresAt != "" {
		doc["expiry"] = map[string]any{"expires_at": expiresAt}
	}
	if len(doc) == 0 {
		return nil, usagef("meta set requires at least one metadata flag (visibility, auth, expiry, --header, --list/--no-list, --crawlable/--no-crawlable, --index/--no-index)")
	}
	return json.Marshal(doc)
}

// buildCrawlPatch assembles the "crawl" section shared by `meta set` and
// `collection create` from their crawl-related flags.
func buildCrawlPatch(crawlable, noCrawlable, index, noIndex bool) (map[string]any, error) {
	if crawlable && noCrawlable {
		return nil, usagef("--crawlable and --no-crawlable are mutually exclusive")
	}
	if index && noIndex {
		return nil, usagef("--index and --no-index are mutually exclusive")
	}
	crawl := map[string]any{}
	if crawlable {
		crawl["crawlable"] = true
	}
	if noCrawlable {
		crawl["crawlable"] = false
	}
	if index {
		crawl["indexable"] = true
	}
	if noIndex {
		crawl["indexable"] = false
	}
	return crawl, nil
}

// parseHeaders parses repeated "k=v" flag values into a map.
func parseHeaders(kvs []string) (map[string]string, error) {
	if len(kvs) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(kvs))
	for _, kv := range kvs {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			return nil, usagef("invalid --header %q: want k=v", kv)
		}
		out[k] = v
	}
	return out, nil
}
