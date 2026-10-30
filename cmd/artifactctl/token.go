package main

import (
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jdelic/artifactsd/pkg/apiclient"
)

// newTokenCmd builds `artifactctl token ...` (design/10 §2).
func (a *app) newTokenCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "token",
		Short: "Manage API tokens",
	}
	cmd.AddCommand(a.newTokenCreateCmd(), a.newTokenListCmd(), a.newTokenRevokeCmd())
	return cmd
}

func (a *app) newTokenCreateCmd() *cobra.Command {
	var (
		scopes []string
		expiry expiryFlags
	)
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create an API token",
		Long: "Create a management API token and print its secret once (the server stores\n" +
			"only a hash). Use it via --token, ARTIFACTSD_TOKEN, or a config profile.\n\n" +
			"Scopes:\n" +
			"  admin               everything; the only scope that can manage tokens and\n" +
			"                      upload requests, read the audit log and stats, run\n" +
			"                      maintenance, or crawl non-crawlable objects\n" +
			"  collections:read    list/show collections, read files, metadata, and crawl\n" +
			"  collections:write   create/delete collections, upload/delete files, set metadata\n" +
			"  shares:read         list and show shares\n" +
			"  shares:write        create/revoke shares\n\n" +
			"A :write scope implies the matching :read.",
		Args: exactArgs(1, "<name>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			cli, err := a.client()
			if err != nil {
				return err
			}
			if len(scopes) == 0 {
				return usagef("token create requires at least one --scope")
			}
			expiresAt, err := expiry.expiresAt()
			if err != nil {
				return err
			}
			resp, err := cli.CreateToken(cmd.Context(), apiclient.CreateTokenRequest{
				Name: args[0], Scopes: scopes, ExpiresAt: expiresAt,
			})
			if err != nil {
				return err
			}
			if a.jsonOutput() {
				return a.printJSON(resp)
			}
			a.printf("token (shown once, save it): %s\n", resp.Secret)
			a.printf("id:     %d\n", resp.ID)
			a.printf("name:   %s\n", resp.Name)
			a.printf("scopes: %s\n", strings.Join(resp.Scopes, ","))
			return nil
		},
	}
	f := cmd.Flags()
	f.StringArrayVar(&scopes, "scope", nil, "token scope (repeatable): admin|collections:read|collections:write|shares:read|shares:write")
	expiry.register(f)
	return cmd
}

func (a *app) newTokenListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List API tokens",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cli, err := a.client()
			if err != nil {
				return err
			}
			page, err := cli.ListTokens(cmd.Context())
			if err != nil {
				return err
			}
			if a.jsonOutput() {
				return a.printJSON(page)
			}
			for _, t := range page.Items {
				status := ""
				if t.RevokedAt != nil {
					status = "\trevoked"
				}
				a.printf("%d\t%s\t%s\t%s%s\n", t.ID, t.Name, t.Prefix, strings.Join(t.Scopes, ","), status)
			}
			return nil
		},
	}
}

func (a *app) newTokenRevokeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "revoke <id>",
		Short: "Revoke an API token",
		Args:  exactArgs(1, "<id>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			cli, err := a.client()
			if err != nil {
				return err
			}
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return usagef("invalid token id %q", args[0])
			}
			if err := cli.RevokeToken(cmd.Context(), id); err != nil {
				return err
			}
			return a.printResult(map[string]any{"revoked": id}, "revoked token %d\n", id)
		},
	}
}
