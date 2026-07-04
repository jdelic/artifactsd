---
name: artifactsd
description: Publish files (HTML reports, PDFs, images, logs, JSON, archives) to an artifactsd server and return shareable URLs. Use when you need to make an artifact you produced reachable over HTTP — public, unlisted, protected, or behind a hard-to-guess expiring share link.
---

# Publishing artifacts with artifactsd

`artifactsd` is a self-hosted server that stores files and serves them over HTTP
with per-object access policy, expiry, and crawl controls. Use it to hand a user
a link to something you made instead of pasting large content inline.

You reach it two ways — pick whichever your environment provides:

- **`artifactctl`** CLI (over the HTTP API). Add `--output json` for parseable
  output.
- **MCP tools** (`artifact_*`), if an artifactsd MCP server is configured.

The model is `collection / folder / file`. A **collection** is the top-level
namespace for one artifact tree. Files are served at
`{base_url}/c/{collection}/{path}`; **shares** are served at unguessable
`{base_url}/s/{share_id}/...` URLs with their own policy and expiry.

## Choose visibility deliberately

| Mode | Who can fetch |
|------|---------------|
| `public` | anyone with the URL; may appear in listings/sitemaps |
| `unlisted` | anyone with the URL, but hidden from listings and indexes |
| `protected` | only with a configured credential (bearer/basic/OIDC) |
| `private` | nobody via public serving — only the management API; expose via a share |

Default to **non-indexable** (`--no-index`) unless the user explicitly wants
search engines to find it. Use **expiry** for anything ephemeral so it cleans
itself up.

## Common tasks (CLI)

Publish a built site as a public, non-indexable report:

```console
artifactctl collection create <name> --public --list --crawlable --no-index
artifactctl put-tree <name> ./dist --content-type-auto
# -> https://<host>/c/<name>/index.html
```

Publish a private result and return a link that expires on its own:

```console
artifactctl collection create <name> --private --expires-in 14d
artifactctl put-tree <name> ./outputs
artifactctl share create <name> --unlisted --expires-in 48h
# prints an unguessable https://<host>/s/<share_id>/ URL — return THIS to the user
```

Publish a single protected file:

```console
artifactctl put <name>/report.pdf ./report.pdf \
  --content-type application/pdf --protected --bearer-token <token>
# fetch with:  curl -H "Authorization: Bearer <token>" https://<host>/c/<name>/report.pdf
```

Ask a third party (human or agent) to send you files — no credentials needed
on their side:

```console
artifactctl upload-request create <name> --target-folder inbox \
  --private --expires-in 7d --use-submission-folders
# prints an unguessable https://<host>/r/<request_id> URL — share THAT;
# they open it in a browser or POST files to it directly
```

Inspect what exists:

```console
artifactctl collection list
artifactctl tree <name>
artifactctl crawl <name>              # policy-filtered view for agents
artifactctl meta get <name>/<path>
artifactctl meta explain <name>/<path>  # which layer set each policy value
```

## Common tasks (MCP)

```
1. artifact_create_collection(name)
2. artifact_put_text(name, "index.html", "<html>...</html>")     # small text inline
3. artifact_begin_upload(name, "report.pdf", "application/pdf")  # large/binary -> upload_url
4. PUT the bytes directly to the returned upload_url
5. artifact_create_share(name, visibility="unlisted", expires_in="7d")
6. return the /s/... share URL to the user
```

Other tools: `artifact_get_metadata`, `artifact_set_metadata`,
`artifact_delete`, `artifact_list_tree`, `artifact_get_public_url`,
`artifact_get_crawl_tree`, and `artifact_create_upload_request` /
`artifact_list_upload_requests` / `artifact_get_upload_request` /
`artifact_revoke_upload_request` for collecting files *from* third parties
via shareable `/r/...` links.

## Guidance

- **Return the right URL.** For a private collection, return the **share**
  (`/s/...`) URL, not the `/c/...` path — the latter won't be fetchable.
- **Large or binary files** go through `begin_upload` / `put-tree`, not inline
  text, so they don't bloat the message channel.
- **Set content types** so browsers render artifacts correctly
  (`--content-type-auto` for trees, `--content-type` for single files).
- **Don't leak secrets.** `--generate-bearer-token` creates a token and prints
  it once; capture it and pass it to whoever needs access. `--bearer-token`
  always takes an explicit value.
- **A missing protected/expired file returns 404, not 403** — that's by design,
  not an error to retry.

Full reference: see `docs/cli.md`, `docs/http-api.md`, and `docs/mcp.md`.
