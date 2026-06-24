package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jdelic/artifactsd/pkg/apiclient"
)

// bearerTokenAutoGen is pflag's NoOptDefVal for --bearer-token: the sentinel
// value it sets when the flag is given with no argument, signaling "generate
// one for me" (design/10 §3). pflag only allows a no-arg invocation for
// string flags when NoOptDefVal is non-empty.
const bearerTokenAutoGen = "\x00generate\x00"

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
		unlisted    bool
		protected   bool
		bearerToken string
		basic       string
		expires     string
		expiresIn   string
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
			genToken := ""
			if bearerToken == bearerTokenAutoGen {
				genToken, err = generateToken()
				if err != nil {
					return err
				}
				bearerToken = genToken
			}
			doc, err := buildShareMetadata(unlisted, protected, bearerToken, basic, expires, expiresIn)
			if err != nil {
				return err
			}
			sh, err := cli.CreateShare(cmd.Context(), collection, apiclient.CreateShareRequest{
				TargetPath: targetPath, Metadata: doc,
			})
			if err != nil {
				return err
			}
			return a.renderShare(sh, genToken, "created share %s\n")
		},
	}
	f := cmd.Flags()
	f.BoolVar(&unlisted, "unlisted", false, "share visibility: unlisted")
	f.BoolVar(&protected, "protected", false, "share visibility: protected (requires --bearer-token or --basic)")
	f.StringVar(&bearerToken, "bearer-token", "", "require a bearer token (omit value to generate one and print it once)")
	f.Lookup("bearer-token").NoOptDefVal = bearerTokenAutoGen
	f.StringVar(&basic, "basic", "", "require basic auth <user:pass>")
	f.StringVar(&expires, "expires", "", "expire at absolute RFC3339 timestamp")
	f.StringVar(&expiresIn, "expires-in", "", "expire after duration (e.g. 30d, 48h, 2w)")
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
func buildShareMetadata(unlisted, protected bool, bearerToken, basic, expires, expiresIn string) (json.RawMessage, error) {
	access := map[string]any{}
	switch {
	case unlisted:
		access["visibility"] = "unlisted"
	case protected:
		access["visibility"] = "protected"
	}
	var auth []map[string]any
	if bearerToken != "" {
		auth = append(auth, map[string]any{"type": "bearer", "token": bearerToken})
	}
	if basic != "" {
		user, pass, ok := strings.Cut(basic, ":")
		if !ok {
			return nil, usagef("invalid --basic %q: want user:pass", basic)
		}
		auth = append(auth, map[string]any{"type": "basic", "username": user, "password": pass})
	}
	if len(auth) > 0 {
		access["auth"] = auth
	}
	doc := map[string]any{}
	if len(access) > 0 {
		doc["access"] = access
	}
	expiresAt, err := expiresAtFrom(expires, expiresIn)
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
