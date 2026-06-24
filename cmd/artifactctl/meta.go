package main

import (
	"encoding/json"
	"strings"

	"github.com/spf13/cobra"
)

// newMetaCmd builds `artifactctl meta ...` (design/10 §2).
func (a *app) newMetaCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "meta",
		Short: "Read or update file metadata",
	}
	cmd.AddCommand(a.newMetaGetCmd(), a.newMetaSetCmd())
	return cmd
}

func (a *app) newMetaGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <collection>/<path>",
		Short: "Show a file's own and effective metadata",
		Args:  exactArgs(1, "<collection>/<path>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			cli, err := a.client()
			if err != nil {
				return err
			}
			collection, path, err := splitTarget(args[0])
			if err != nil {
				return err
			}
			meta, err := cli.GetFileMeta(cmd.Context(), collection, path)
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
		visibility string
		expires    string
		headers    []string
		crawlable  bool
		index      bool
		noIndex    bool
	)
	cmd := &cobra.Command{
		Use:   "set <collection>/<path>",
		Short: "Update a file's metadata",
		Args:  exactArgs(1, "<collection>/<path>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			cli, err := a.client()
			if err != nil {
				return err
			}
			collection, path, err := splitTarget(args[0])
			if err != nil {
				return err
			}
			patch, err := buildFileMetaPatch(visibility, expires, headers, crawlable, index, noIndex)
			if err != nil {
				return err
			}
			meta, err := cli.PatchFileMeta(cmd.Context(), collection, path, patch)
			if err != nil {
				return err
			}
			if a.jsonOutput() {
				return a.printJSON(meta)
			}
			a.printf("updated metadata for %s\n", meta.Path)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&visibility, "visibility", "", "visibility: public|unlisted|protected|private")
	f.StringVar(&expires, "expires", "", "expire at absolute RFC3339 timestamp")
	f.StringArrayVar(&headers, "header", nil, "response header k=v (repeatable)")
	f.BoolVar(&crawlable, "crawlable", false, "mark crawlable")
	f.BoolVar(&index, "index", false, "mark indexable")
	f.BoolVar(&noIndex, "no-index", false, "mark non-indexable")
	return cmd
}

// buildFileMetaPatch assembles a metadata document patch from the `meta set`
// flags (design/03 §2 for the document shape).
func buildFileMetaPatch(visibility, expires string, headers []string, crawlable, index, noIndex bool) (json.RawMessage, error) {
	if visibility != "" && !validVisibility(visibility) {
		return nil, usagef("invalid --visibility %q: want public|unlisted|protected|private", visibility)
	}
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
	if expires != "" {
		doc["expiry"] = map[string]any{"expires_at": expires}
	}
	if len(doc) == 0 {
		return nil, usagef("meta set requires at least one of --visibility, --expires, --header, --crawlable, --index, --no-index")
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
