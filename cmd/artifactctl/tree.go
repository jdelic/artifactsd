package main

import (
	"strings"

	"github.com/spf13/cobra"
)

// newTreeCmd builds `artifactctl tree <collection> [--path] [--depth]`
// (design/10 §2).
func (a *app) newTreeCmd() *cobra.Command {
	var (
		path  string
		depth int
	)
	cmd := &cobra.Command{
		Use:   "tree <collection>",
		Short: "Show a collection's structured listing",
		Args:  exactArgs(1, "<collection>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			cli, err := a.client()
			if err != nil {
				return err
			}
			page, err := cli.Tree(cmd.Context(), args[0], path, depth)
			if err != nil {
				return err
			}
			if a.jsonOutput() {
				return a.printJSON(page)
			}
			for _, n := range page.Items {
				indent := strings.Repeat("  ", strings.Count(n.Path, "/"))
				if n.Kind == "folder" {
					a.printf("%s%s/\n", indent, n.Path)
					continue
				}
				a.printf("%s%s\n", indent, n.Path)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&path, "path", "", "scope the listing to this subtree")
	f.IntVar(&depth, "depth", 0, "limit how many levels below --path are returned (0 = unlimited)")
	return cmd
}
