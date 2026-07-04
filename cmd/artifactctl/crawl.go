package main

import (
	"strings"

	"github.com/spf13/cobra"
)

// newCrawlCmd builds `artifactctl crawl <collection> [--path] [--depth]`
// (design/10 §2): the authenticated agent crawl API (SPEC §10.5), as an
// alternative to scraping HTML directory listings.
func (a *app) newCrawlCmd() *cobra.Command {
	var (
		path  string
		depth int
	)
	cmd := &cobra.Command{
		Use:   "crawl <collection>",
		Short: "Fetch a collection's crawl tree",
		Args:  exactArgs(1, "<collection>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			cli, err := a.client()
			if err != nil {
				return err
			}
			if depth < 0 {
				return usagef("--depth must be >= 0 (0 = full tree)")
			}
			resp, err := cli.Crawl(cmd.Context(), args[0], path, depth)
			if err != nil {
				return err
			}
			if a.jsonOutput() {
				return a.printJSON(resp)
			}
			a.printf("collection: %s\n", resp.Collection)
			a.printf("base_url:   %s\n", resp.BaseURL)
			a.printf("crawlable:  %v\n", resp.Crawlable)
			a.printf("indexable:  %v\n", resp.Indexable)
			for _, e := range resp.Entries {
				indent := strings.Repeat("  ", strings.Count(strings.TrimSuffix(e.Path, "/"), "/"))
				a.printf("%s%s\n", indent, e.Path)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&path, "path", "", "scope the crawl to this subtree")
	f.IntVar(&depth, "depth", 0, "limit how many levels below --path are returned (0 = full tree)")
	return cmd
}
