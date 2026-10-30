package main

// rootLong is the top-level help text. The concepts shared across commands
// (connection, tokens, visibility, auth, listing/crawl flags, shares) are
// explained once here and referenced from the flags that set them, rather
// than repeated per command.
const rootLong = `artifactctl — command-line client for an artifactsd server.

Connecting
  Every server-backed command needs a base URL and a management API token.
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
  secret in shell history and the process list.

Tokens
  The token is an API token, not the admin UI's login. The server seeds one
  "admin"-scoped token from ARTIFACTSD_BOOTSTRAP_TOKEN at startup; mint narrower
  ones with "artifactctl token create --scope ..." (its --help lists the
  scopes).

Visibility
  Every collection, folder, and file has metadata that controls who can fetch
  it from the public routes (/c/... and share links /s/...). The management
  API always returns whatever the token's scopes allow.

  public     Served to anyone with the URL. The default.
  unlisted   Served to anyone with the URL, but hidden from directory
             listings, robots.txt, and the sitemap.
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

Shares
  A share (/s/<id>/...) is an unguessable capability link to a collection,
  folder, or file. Possessing the link grants access: private targets are
  served through it (as unlisted). Auth configured on the target still
  applies unless the share sets its own (--protected with an auth flag), and
  expired objects stay 404.

Use "artifactctl meta explain <collection>[/<path>]" to see the effective
settings for an object and which layer each one came from.`
