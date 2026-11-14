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
	cmd.AddCommand(a.newTokenCreateCmd(), a.newTokenListCmd(), a.newTokenRevokeCmd(),
		a.newTokenGrantCmd(), a.newTokenUngrantCmd())
	return cmd
}

func (a *app) newTokenCreateCmd() *cobra.Command {
	var (
		scopes     []string
		grants     []string
		restricted bool
		expiry     expiryFlags
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
			"  collections:write   create collections, upload/replace files, set metadata\n" +
			"  collections:delete  delete collections, folders and files\n" +
			"  shares:read         list and show shares\n" +
			"  shares:write        create shares and change their policy\n" +
			"  shares:delete       revoke shares\n\n" +
			"A :write or :delete scope implies the matching :read; :write does NOT imply\n" +
			":delete, so grant delete only to tokens that need it.\n\n" +
			"Restricted tokens (--restricted) reach only the collections they are granted,\n" +
			"with scopes per collection instead of --scope; any other collection looks\n" +
			"like it does not exist. They cannot create collections or be admin:\n\n" +
			"  artifactctl token create agent --restricted \\\n" +
			"      --grant reports=collections:read,collections:write\n\n" +
			"Change grants later with `token grant` / `token ungrant`.",
		Args: exactArgs(1, "<name>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			cli, err := a.client()
			if err != nil {
				return err
			}
			req := apiclient.CreateTokenRequest{Name: args[0], Scopes: scopes, Restricted: restricted}
			switch {
			case restricted && len(scopes) > 0:
				return usagef("--restricted tokens take --grant <collection>=<scopes>, not --scope")
			case !restricted && len(grants) > 0:
				return usagef("--grant requires --restricted")
			case !restricted && len(scopes) == 0:
				return usagef("token create requires at least one --scope (or --restricted with --grant)")
			}
			for _, g := range grants {
				parsed, err := parseGrantFlag(g)
				if err != nil {
					return err
				}
				req.Grants = append(req.Grants, parsed)
			}
			if req.ExpiresAt, err = expiry.expiresAt(); err != nil {
				return err
			}
			resp, err := cli.CreateToken(cmd.Context(), req)
			if err != nil {
				return err
			}
			if a.jsonOutput() {
				return a.printJSON(resp)
			}
			a.printf("token (shown once, save it): %s\n", resp.Secret)
			a.printf("id:     %d\n", resp.ID)
			a.printf("name:   %s\n", resp.Name)
			a.printf("scopes: %s\n", tokenAccess(&resp.Token))
			return nil
		},
	}
	f := cmd.Flags()
	f.StringArrayVar(&scopes, "scope", nil, "token scope (repeatable): admin|collections:read|collections:write|collections:delete|shares:read|shares:write|shares:delete")
	f.BoolVar(&restricted, "restricted", false, "limit the token to the collections given with --grant")
	f.StringArrayVar(&grants, "grant", nil, "grant on one collection (repeatable, with --restricted): <collection>=<scope>[,<scope>...]")
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
			for i := range page.Items {
				t := &page.Items[i]
				status := ""
				if t.RevokedAt != nil {
					status = "\trevoked"
				}
				a.printf("%d\t%s\t%s\t%s%s\n", t.ID, t.Name, t.Prefix, tokenAccess(t), status)
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

func (a *app) newTokenGrantCmd() *cobra.Command {
	var scopes []string
	cmd := &cobra.Command{
		Use:   "grant <id> <collection>",
		Short: "Grant a restricted token scopes on a collection",
		Long: "Create or replace a restricted token's grant on one collection. The given\n" +
			"scopes replace the grant's previous ones. Grant scopes: collections:read,\n" +
			"collections:write, collections:delete, shares:read, shares:write,\n" +
			"shares:delete (never admin).",
		Args: exactArgs(2, "<id> <collection>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return usagef("invalid token id %q", args[0])
			}
			if len(scopes) == 0 {
				return usagef("token grant requires at least one --scope")
			}
			cli, err := a.client()
			if err != nil {
				return err
			}
			tok, err := cli.SetTokenGrant(cmd.Context(), id, args[1], scopes)
			if err != nil {
				return err
			}
			if a.jsonOutput() {
				return a.printJSON(tok)
			}
			a.printf("token %d: %s\n", tok.ID, tokenAccess(tok))
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&scopes, "scope", nil, "grant scope (repeatable): collections:read|collections:write|collections:delete|shares:read|shares:write|shares:delete")
	return cmd
}

func (a *app) newTokenUngrantCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ungrant <id> <collection>",
		Short: "Remove a restricted token's grant on a collection",
		Args:  exactArgs(2, "<id> <collection>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return usagef("invalid token id %q", args[0])
			}
			cli, err := a.client()
			if err != nil {
				return err
			}
			if err := cli.DeleteTokenGrant(cmd.Context(), id, args[1]); err != nil {
				return err
			}
			return a.printResult(map[string]any{"ungranted": id, "collection": args[1]},
				"removed token %d's grant on %s\n", id, args[1])
		},
	}
}

// parseGrantFlag parses --grant <collection>=<scope>[,<scope>...].
func parseGrantFlag(s string) (apiclient.Grant, error) {
	coll, list, ok := strings.Cut(s, "=")
	if !ok || coll == "" || list == "" {
		return apiclient.Grant{}, usagef("invalid --grant %q: want <collection>=<scope>[,<scope>...]", s)
	}
	g := apiclient.Grant{Collection: coll}
	for sc := range strings.SplitSeq(list, ",") {
		if sc = strings.TrimSpace(sc); sc != "" {
			g.Scopes = append(g.Scopes, sc)
		}
	}
	return g, nil
}

// tokenAccess renders what a token may do: its scopes, or for a restricted
// token "restricted: coll=scope,scope; coll2=..." (design/16 §7).
func tokenAccess(t *apiclient.Token) string {
	if !t.Restricted {
		return strings.Join(t.Scopes, ",")
	}
	if len(t.Grants) == 0 {
		return "restricted: no grants"
	}
	parts := make([]string, 0, len(t.Grants))
	for _, g := range t.Grants {
		parts = append(parts, g.Collection+"="+strings.Join(g.Scopes, ","))
	}
	return "restricted: " + strings.Join(parts, "; ")
}
