# Quickstart

This walks through running the server and publishing artifacts three ways:
a public report, a private run with an expiring share, and a protected file.
It assumes you have built `artifactsd` and `artifactctl`
([Installation](installation.md)).

## 1. Start the server

For local experimentation, plain HTTP on `localhost` is enough. The management
API needs an admin token to mint further tokens, so seed one at boot with
`ARTIFACTSD_BOOTSTRAP_TOKEN`:

```console
$ export ARTIFACTSD_BOOTSTRAP_TOKEN=dev-admin-secret
$ artifactsd serve \
    --listen :8080 \
    --base-url http://localhost:8080 \
    --data ./artifactsd-data
```

The bootstrap token is seeded idempotently as an `admin`-scoped API token on
every boot. Treat it like any secret; once you've created a "real" admin token
you can drop it from the environment.

## 2. Point the CLI at the server

`artifactctl` reads its server URL and token from flags, environment, or a
config file. The quickest path is environment variables:

```console
$ export ARTIFACTSD_BASE_URL=http://localhost:8080
$ export ARTIFACTSD_TOKEN=dev-admin-secret
```

Or save a profile so you don't have to repeat them
(`~/.config/artifactctl/config.yaml`):

```yaml
current: default
profiles:
  default:
    base_url: http://localhost:8080
    token: dev-admin-secret
```

Verify connectivity:

```console
$ artifactctl collection list
```

## 3. Publish a public report (not indexable)

Create a public, browsable, crawlable but non-indexable collection, then upload
a built site directory into it:

```console
$ artifactctl collection create quarterly-report \
    --public --list --crawlable --no-index

$ artifactctl put-tree quarterly-report ./dist --content-type-auto
```

Your report is now live, with content types inferred per file:

```
http://localhost:8080/c/quarterly-report/index.html
```

Search engines are discouraged from indexing it (via `robots.txt` and
`X-Robots-Tag: noindex`), but authorized agents can still enumerate the tree
through the crawl API.

## 4. Private run with an expiring share

Agent runs are often private but need to be handed to one person briefly. Create
a private collection that self-deletes, upload outputs, then mint an unguessable
share URL that expires on its own:

```console
$ artifactctl collection create run-8f31c2 --private --expires-in 14d

$ artifactctl put-tree run-8f31c2 ./outputs

$ artifactctl share create run-8f31c2 --unlisted --expires-in 48h
```

This prints a hard-to-guess share URL:

```
http://localhost:8080/s/7V9qWb3KxLm2/
```

The collection stays private (hidden from public serving) while the share is
reachable by anyone who has the link — until it expires in 48 hours, or the
collection is reaped in 14 days, whichever comes first.

## 5. Protected file behind a bearer token

```console
$ artifactctl put client-demo/report.pdf ./report.pdf \
    --content-type application/pdf \
    --protected --bearer-token demo-token
```

Fetching now requires the token:

```console
$ curl -H "Authorization: Bearer demo-token" \
    http://localhost:8080/c/client-demo/report.pdf
```

Without the header the server returns `404 Not Found` (not `403`) so it doesn't
leak the file's existence.

> Pass `--bearer-token` with no value to have the CLI generate a random token
> and print it once.

## 6. Inspect what's there

```console
$ artifactctl tree quarterly-report          # structured listing
$ artifactctl crawl quarterly-report          # policy-filtered crawl view
$ artifactctl meta get quarterly-report/index.html
```

Add `--output json` to any command for machine-readable output — handy in
scripts and for agents.

## Where to go next

- [CLI reference](cli.md) — every command and flag.
- [HTTP API reference](http-api.md) — what the CLI calls under the hood.
- [Configuration](configuration.md) — TLS, admin UI, OIDC, limits, expiry.
- [MCP adapter](mcp.md) — drive all of this from an AI agent.
