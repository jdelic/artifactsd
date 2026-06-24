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
		scopes    []string
		expiresIn string
	)
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create an API token",
		Args:  exactArgs(1, "<name>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			cli, err := a.client()
			if err != nil {
				return err
			}
			if len(scopes) == 0 {
				return usagef("token create requires at least one --scope")
			}
			expiresAt, err := expiresAtFrom("", expiresIn)
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
	f.StringArrayVar(&scopes, "scope", nil, "token scope (repeatable): admin|collections:read|collections:write|shares:read|shares:write|tokens:read|tokens:write")
	f.StringVar(&expiresIn, "expires-in", "", "expire after duration (e.g. 30d, 48h, 2w)")
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
				a.printf("%d\t%s\t%s\t%s\n", t.ID, t.Name, t.Prefix, strings.Join(t.Scopes, ","))
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
			a.printf("revoked token %d\n", id)
			return nil
		},
	}
}
