# MCP adapter

The MCP adapter exposes a small set of agent-friendly tools over the
[HTTP API](http-api.md). **MCP is an adapter, not the canonical storage layer** —
everything it does is also reachable via the API and `artifactctl`.

## Running it

```console
$ artifactsd mcp \
    --listen :8081 \
    --base-url https://artifacts.example.com \
    --token tok_...
```

The adapter is a thin HTTP-to-HTTP bridge: it serves MCP over **Streamable
HTTP** and calls the artifactsd management API as a client, using the configured
`--token`. Because it's network-reachable, possibly-remote agents can use it
directly. It can run inside the main process or as a separate sidecar — its
default port (`:8081`) differs from `serve` (`:8080`) so both can run on one
host. See [Configuration → MCP adapter](configuration.md#mcp-adapter).

The token the adapter holds determines what agents can do through it: give it
only the scopes you want exposed.

## Tools

| Tool | Maps to | Notes |
|------|---------|-------|
| `artifact_create_collection` | `POST /collections` | name + policy flags |
| `artifact_put_text` | `PUT .../files/{path}` | small text artifacts inline |
| `artifact_begin_upload` | `POST .../uploads` | returns a direct upload URL for large/binary |
| `artifact_get_metadata` | `GET .../meta` | effective + own |
| `artifact_set_metadata` | `PATCH .../meta` | merge |
| `artifact_delete` | `DELETE .../files/{path}` | |
| `artifact_list_tree` | `GET .../tree` or `/crawl` | structured listing |
| `artifact_get_public_url` | local compose | builds a `/c/...` URL |
| `artifact_create_share` | `POST .../shares` | returns a `/s/...` URL |
| `artifact_get_crawl_tree` | `GET .../crawl` | policy-filtered enumeration |

Tool inputs and outputs are JSON with explicit schemas, so agents get validation
and discoverability. API error codes are mapped to MCP tool errors with
actionable messages, and returned URLs are absolute (built from the server
`base_url`).

## Upload strategy

- **Small text** → `artifact_put_text` (the content is an inline string in the
  tool call).
- **Large or binary** → `artifact_begin_upload` returns an HTTP `upload_url`;
  the agent `PUT`s the bytes directly, out of band, then optionally
  `artifact_create_share`. This keeps large binaries out of the MCP message
  channel.

## Example flow

```
1. artifact_create_collection("run-8f31c2")
2. artifact_put_text("run-8f31c2", "index.html", "<html>...</html>")
3. artifact_begin_upload("run-8f31c2", "report.pdf", "application/pdf") -> upload_url
4. agent PUTs PDF bytes to upload_url
5. artifact_create_share("run-8f31c2", visibility="unlisted", expires_in="7d") -> /s/...
6. agent returns the share URL to the user
```
