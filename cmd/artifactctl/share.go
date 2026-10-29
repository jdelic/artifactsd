package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jdelic/artifactsd/pkg/apiclient"
)

// newShareCmd builds `artifactctl share ...` (design/10 §2).
func (a *app) newShareCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "share",
		Short: "Create and manage shares",
	}
	cmd.AddCommand(a.newShareCreateCmd(), a.newShareListCmd(), a.newShareRevokeCmd())
	return cmd
}

func (a *app) newShareCreateCmd() *cobra.Command {
	var (
		unlisted  bool
		protected bool
		auth      authFlags
		expiry    expiryFlags
	)
	cmd := &cobra.Command{
		Use:   "create <collection>[/<path>]",
		Short: "Create a share for a collection, folder, or file",
		Args:  exactArgs(1, "<collection>[/<path>]"),
		RunE: func(cmd *cobra.Command, args []string) error {
			cli, err := a.client()
			if err != nil {
				return err
			}
			if unlisted && protected {
				return usagef("--unlisted and --protected are mutually exclusive")
			}
			collection, targetPath := splitShareTarget(args[0])
			doc, err := buildShareMetadata(unlisted, protected, &auth, &expiry)
			if err != nil {
				return err
			}
			sh, err := cli.CreateShare(cmd.Context(), collection, apiclient.CreateShareRequest{
				TargetPath: targetPath, Metadata: doc,
			})
			if err != nil {
				return err
			}
			return a.renderShare(sh, auth.genToken, "created share %s\n")
		},
	}
	f := cmd.Flags()
	f.BoolVar(&unlisted, "unlisted", false, "share visibility: unlisted (hidden from listings and sitemap)")
	f.BoolVar(&protected, "protected", false, "share visibility: protected (requires --bearer-token, --generate-bearer-token, or --basic)")
	auth.register(f)
	expiry.register(f)
	return cmd
}

func (a *app) newShareListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list <collection>",
		Short: "List a collection's shares",
		Args:  exactArgs(1, "<collection>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			cli, err := a.client()
			if err != nil {
				return err
			}
			page, err := cli.ListShares(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if a.jsonOutput() {
				return a.printJSON(page)
			}
			for i := range page.Items {
				sh := &page.Items[i]
				a.printf("%s\t%s\t%s\n", sh.ShareID, sh.TargetPath, sh.URL)
			}
			return nil
		},
	}
}

func (a *app) newShareRevokeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "revoke <share-id>",
		Short: "Revoke a share",
		Args:  exactArgs(1, "<share-id>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			cli, err := a.client()
			if err != nil {
				return err
			}
			if err := cli.RevokeShare(cmd.Context(), args[0]); err != nil {
				return err
			}
			a.printf("revoked share %s\n", args[0])
			return nil
		},
	}
}

// renderShare prints a share as JSON or a short text summary. textMsg (if
// non-empty) is a printf-style line shown before the text summary. genToken,
// if set, is a --bearer-token value we generated client-side: the server only
// ever stores its hash, so this is the only chance to show it to the user.
func (a *app) renderShare(sh *apiclient.Share, genToken, textMsg string) error {
	if a.jsonOutput() {
		if genToken == "" {
			return a.printJSON(sh)
		}
		return a.printJSON(struct {
			apiclient.Share
			GeneratedBearerToken string `json:"generated_bearer_token"`
		}{Share: *sh, GeneratedBearerToken: genToken})
	}
	if textMsg != "" {
		a.printf(textMsg, sh.ShareID)
	}
	if genToken != "" {
		a.printf("bearer token (shown once, save it): %s\n", genToken)
	}
	a.printf("url:    %s\n", sh.URL)
	a.printf("target: %s/%s\n", sh.Collection, sh.TargetPath)
	return nil
}

// splitShareTarget splits "collection[/path]" into its parts; path is "" when
// absent, sharing the whole collection.
func splitShareTarget(arg string) (collection, path string) {
	collection, path, _ = strings.Cut(arg, "/")
	return collection, path
}

// buildShareMetadata assembles a share's metadata document from the create
// flags.
func buildShareMetadata(unlisted, protected bool, auth *authFlags, expiry *expiryFlags) (json.RawMessage, error) {
	access := map[string]any{}
	switch {
	case unlisted:
		access["visibility"] = "unlisted"
	case protected:
		access["visibility"] = "protected"
	}
	entries, err := auth.entries()
	if err != nil {
		return nil, err
	}
	if len(entries) > 0 {
		access["auth"] = entries
	}
	doc := map[string]any{}
	if len(access) > 0 {
		doc["access"] = access
	}
	expiresAt, err := expiry.expiresAt()
	if err != nil {
		return nil, err
	}
	if expiresAt != "" {
		doc["expiry"] = map[string]any{"expires_at": expiresAt}
	}
	if len(doc) == 0 {
		return nil, nil
	}
	return json.Marshal(doc)
}

// generateToken returns a random hex token suitable for --bearer-token's
// generated-secret form.
func generateToken() (string, error) {
	buf := make([]byte, 20)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
