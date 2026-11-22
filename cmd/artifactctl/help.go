package main

import "github.com/spf13/cobra"

// Help texts for concepts shared across commands. Each one lives on the
// command where a user is looking for it (visibility and auth on "meta",
// shares on "share create", tokens on "token", connection setup on the "config"
// help topic) and flags elsewhere point there, so the root help stays a short
// orientation instead of a manual.

// rootLong is the top-level help text: what the tool is and how to connect.
const rootLong = `artifactctl — command-line client for an artifactsd server.

Every server-backed command needs the server's base URL and a management API
token, e.g. from the environment:

  export ARTIFACTSD_BASE_URL=https://artifacts.example.com
  export ARTIFACTSD_TOKEN=tok_...

See "artifactctl help config" for flags, config file profiles, and
precedence, and "artifactctl token --help" for where tokens come from.`

// configLong documents connection setup (the "config" help topic).
const configLong = `Every server-backed command needs a base URL and a management API token.
Each is resolved in this order, highest precedence first:

  1. flags          --base-url, --token
  2. environment    ARTIFACTSD_BASE_URL, ARTIFACTSD_TOKEN
  3. config file    the profile named by --profile, else the file's "current"

The config file lives at $ARTIFACTCTL_CONFIG, else
$XDG_CONFIG_HOME/artifactctl/config.yaml, else
~/.config/artifactctl/config.yaml:

  current: prod
  profiles:
    prod:
      base_url: https://artifacts.example.com
      token: tok_...

Prefer ARTIFACTSD_TOKEN or the config file over --token, which leaves the
secret in shell history and the process list.`

// tokenLong explains what management API tokens are.
const tokenLong = `Manage management API tokens.

The token artifactctl uses (--token, ARTIFACTSD_TOKEN, or a config profile) is
an API token, not the admin UI's login. The server seeds one "admin"-scoped
token from ARTIFACTSD_BOOTSTRAP_TOKEN at startup; mint narrower ones with
"artifactctl token create --scope ..." (its --help lists the scopes).`

// metaLong explains the metadata that controls access: visibility, object
// auth, and the listing/crawl/index flags. The commands that set them
// (collection create, put, put-tree, meta set) point here.
const metaLong = `Read, update, or explain collection, folder, and file metadata.

Visibility
  Every collection, folder, and file has metadata that controls who can fetch
  it from the public routes (/c/... and share links /s/...). The management
  API always returns whatever the token's scopes allow.

  public     Served to anyone with the URL. The default.
  unlisted   Served to anyone with the URL, but hidden from its parent's
             directory listing, robots.txt, and the sitemap. A listed folder
             still shows contents that only inherit unlisted from it.
  protected  Served only to callers that pass the object's auth entries (see
             below). A protected object without any auth entry is never
             served (404) until auth is added.
  private    Never served on /c/ routes: always 404, even with valid auth.
             Reachable through the management API and share links.

  Visibility is inherited (collection -> folder -> file, nearest wins), except
  that anything under a private ancestor is private: a public file inside a
  private folder does not leak.

Object auth (--bearer-token, --generate-bearer-token, --basic, --clear-auth)
  Set on collection create, meta set, put, put-tree, and share create. Auth
  entries are any-of: one matching credential is enough. Auth is inherited
  like visibility, and setting it on a child replaces the inherited list
  (--clear-auth on "meta set" sets an empty list). Any object with auth
  requires it, whatever its visibility. An unauthenticated request is
  answered according to the auth type:
    basic    401 with a browser login dialog
    bearer   404, so the object's existence is not revealed
  --generate-bearer-token prints the token once; the server keeps only a hash.

Listing and crawl flags (all off by default)
  --list        the folder URL (trailing slash) shows a directory listing
  --crawlable   included in the crawl tree ("artifactctl crawl"); non-crawlable
                objects appear there only for admin-scoped tokens
  --index       allowed in robots.txt and the sitemap; otherwise served with
                X-Robots-Tag: noindex

  So a default public object is fetchable by its URL but not listed, crawled,
  or indexed.

Use "artifactctl meta explain <collection>[/<path>]" to see the effective
settings for an object and which layer each one came from.`

// seeMetaHelp is appended to commands that take the visibility, auth, and
// listing flags documented in metaLong.
const seeMetaHelp = "\n\nVisibility, auth, and listing flags are explained in \"artifactctl meta --help\"."

// shareCreateLong explains what a share grants.
const shareCreateLong = `Create a share for a collection, folder, or file.

A share (/s/<id>/...) is an unguessable capability link to its target.
Possessing the link grants access: private targets are served through it (as
unlisted). Auth configured on the target still applies unless the share sets
its own (--protected with an auth flag), and expired objects stay 404.` + seeMetaHelp

// newConfigHelpTopic is the "artifactctl help config" topic. It has no Run, so
// cobra lists it under "Additional help topics" rather than as a command.
func newConfigHelpTopic() *cobra.Command {
	return &cobra.Command{
		Use:   "config",
		Short: "How artifactctl finds its server URL and token",
		Long:  configLong,
	}
}
