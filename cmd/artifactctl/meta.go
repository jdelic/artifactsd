package main

import (
	"encoding/json"
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
		Short: "Read or update collection, folder, and file metadata",
	}
	cmd.AddCommand(a.newMetaGetCmd(), a.newMetaSetCmd())
	return cmd
}

func (a *app) newMetaGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <collection>[/<path>]",
		Short: "Show an object's own and effective metadata",
		Long: "Show own and effective metadata for a collection (bare name), a folder\n" +
			"(trailing slash), or a file.",
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
			var meta *apiclient.FileMeta
			switch kind {
			case targetCollection:
				meta, err = cli.GetCollectionMeta(cmd.Context(), collection)
			case targetFolder:
				meta, err = cli.GetFolderMeta(cmd.Context(), collection, path)
			default:
				meta, err = cli.GetFileMeta(cmd.Context(), collection, path)
			}
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

func (a *app) newMetaSetCmd() *cobra.Command {
	var (
		vis       visibilityFlags
		expiry    expiryFlags
		headers   []string
		crawlable bool
		index     bool
		noIndex   bool
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
			patch, err := buildFileMetaPatch(visibility, expiresAt, headers, crawlable, index, noIndex)
			if err != nil {
				return err
			}
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
	expiry.register(f)
	f.StringArrayVar(&headers, "header", nil, "response header k=v (repeatable)")
	f.BoolVar(&crawlable, "crawlable", false, "mark crawlable")
	f.BoolVar(&index, "index", false, "mark indexable")
	f.BoolVar(&noIndex, "no-index", false, "mark non-indexable")
	return cmd
}

// buildFileMetaPatch assembles a metadata document patch from the `meta set`
// flags (design/03 §2 for the document shape); visibility and expiresAt arrive
// already resolved.
func buildFileMetaPatch(visibility, expiresAt string, headers []string, crawlable, index, noIndex bool) (json.RawMessage, error) {
	crawl, err := buildCrawlPatch(crawlable, index, noIndex)
	if err != nil {
		return nil, err
	}
	hdrMap, err := parseHeaders(headers)
	if err != nil {
		return nil, err
	}
	doc := map[string]any{}
	if visibility != "" {
		doc["access"] = map[string]any{"visibility": visibility}
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
		return nil, usagef("meta set requires at least one of --visibility (or a shorthand), --expires, --expires-in, --header, --crawlable, --index, --no-index")
	}
	return json.Marshal(doc)
}

// buildCrawlPatch assembles the "crawl" section shared by `meta set` and
// `collection create` from their crawl-related flags.
func buildCrawlPatch(crawlable, index, noIndex bool) (map[string]any, error) {
	if index && noIndex {
		return nil, usagef("--index and --no-index are mutually exclusive")
	}
	crawl := map[string]any{}
	if crawlable {
		crawl["crawlable"] = true
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
