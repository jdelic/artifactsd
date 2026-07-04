package main

import (
	"encoding/json"

	"github.com/spf13/cobra"

	"github.com/jdelic/artifactsd/pkg/apiclient"
)

// newCollectionCmd builds `artifactctl collection ...` (design/10 §2).
func (a *app) newCollectionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "collection",
		Aliases: []string{"col", "collections"},
		Short:   "Manage collections",
	}
	cmd.AddCommand(
		a.newCollectionCreateCmd(),
		a.newCollectionListCmd(),
		a.newCollectionShowCmd(),
		a.newCollectionDeleteCmd(),
	)
	return cmd
}

func (a *app) newCollectionCreateCmd() *cobra.Command {
	var (
		vis       visibilityFlags
		expiry    expiryFlags
		list      bool
		noList    bool
		crawlable bool
		index     bool
		noIndex   bool
	)
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a collection",
		Args:  exactArgs(1, "<name>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			cli, err := a.client()
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
			meta, err := buildCollectionMetadata(visibility, expiresAt, list, noList, crawlable, index, noIndex)
			if err != nil {
				return err
			}
			col, err := cli.CreateCollection(cmd.Context(), apiclient.CreateCollectionRequest{
				Name: args[0], Metadata: meta,
			})
			if err != nil {
				return err
			}
			return a.renderCollection(col, "created collection %s\n")
		},
	}
	f := cmd.Flags()
	vis.register(f)
	expiry.register(f)
	f.BoolVar(&list, "list", false, "enable directory listing")
	f.BoolVar(&noList, "no-list", false, "disable directory listing")
	f.BoolVar(&crawlable, "crawlable", false, "mark crawlable")
	f.BoolVar(&index, "index", false, "mark indexable")
	f.BoolVar(&noIndex, "no-index", false, "mark non-indexable")
	return cmd
}

func (a *app) newCollectionListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List collections",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cli, err := a.client()
			if err != nil {
				return err
			}
			page, err := cli.ListCollections(cmd.Context(), "", 0)
			if err != nil {
				return err
			}
			if a.jsonOutput() {
				return a.printJSON(page)
			}
			for _, col := range page.Items {
				a.printf("%s\t%s\n", col.Name, col.Visibility)
			}
			return nil
		},
	}
}

func (a *app) newCollectionShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <name>",
		Short: "Show a collection",
		Args:  exactArgs(1, "<name>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			cli, err := a.client()
			if err != nil {
				return err
			}
			col, err := cli.GetCollection(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return a.renderCollection(col, "")
		},
	}
}

func (a *app) newCollectionDeleteCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:     "delete <name>",
		Aliases: []string{"rm", "del"},
		Short:   "Delete a collection",
		Args:    exactArgs(1, "<name>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			cli, err := a.client()
			if err != nil {
				return err
			}
			if err := cli.DeleteCollection(cmd.Context(), args[0], force); err != nil {
				return err
			}
			a.printf("deleted collection %s\n", args[0])
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "delete even if non-empty (cascade)")
	return cmd
}

// renderCollection prints a collection as JSON or a short text summary. textMsg
// (if non-empty) is a printf-style line shown before the text summary.
func (a *app) renderCollection(col *apiclient.Collection, textMsg string) error {
	if a.jsonOutput() {
		return a.printJSON(col)
	}
	if textMsg != "" {
		a.printf(textMsg, col.Name)
	}
	a.printf("name:       %s\n", col.Name)
	a.printf("visibility: %s\n", col.Visibility)
	if col.ExpiresAt != nil {
		a.printf("expires_at: %s\n", *col.ExpiresAt)
	}
	return nil
}

// buildCollectionMetadata assembles a metadata document from the create flags
// (visibility and expiresAt already resolved), or nil when no flag is set
// (server applies defaults).
func buildCollectionMetadata(visibility, expiresAt string, list, noList, crawlable, index, noIndex bool) (json.RawMessage, error) {
	if list && noList {
		return nil, usagef("--list and --no-list are mutually exclusive")
	}
	crawl, err := buildCrawlPatch(crawlable, index, noIndex)
	if err != nil {
		return nil, err
	}
	access := map[string]any{}
	if visibility != "" {
		access["visibility"] = visibility
	}
	if list {
		access["directory_listing"] = true
	}
	if noList {
		access["directory_listing"] = false
	}
	doc := map[string]any{}
	if len(access) > 0 {
		doc["access"] = access
	}
	if len(crawl) > 0 {
		doc["crawl"] = crawl
	}
	if expiresAt != "" {
		doc["expiry"] = map[string]any{"expires_at": expiresAt}
	}
	if len(doc) == 0 {
		return nil, nil
	}
	return json.Marshal(doc)
}

func validVisibility(v string) bool {
	switch v {
	case "public", "unlisted", "protected", "private":
		return true
	default:
		return false
	}
}

// exactArgs is cobra.ExactArgs but reports the shortfall as a usage error so
// main maps it to exit code 2.
func exactArgs(n int, usage string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) != n {
			return usagef("%s requires %s", cmd.CommandPath(), usage)
		}
		return nil
	}
}
