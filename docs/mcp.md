# MCP adapter

The MCP adapter exposes a small set of agent-friendly tools over the
[HTTP API](http-api.md). **MCP is an adapter, not the canonical storage layer** —
everything it does is also reachable via the API and `artifactctl`.

## Running it

```console
$ artifactsd mcp --base-url https://artifacts.example.com
```

The adapter is a thin HTTP-to-HTTP bridge: it serves MCP over **Streamable
HTTP** and calls the artifactsd management API as a client. **It stores no
credentials.** Every MCP client must send its **own** artifactsd API token as
`Authorization: Bearer <token>`; the adapter forwards that token, so each agent
can do exactly what its token's scopes allow, and nothing more. A request
without a token gets `401` (`WWW-Authenticate: Bearer realm="artifactsd-mcp"`);
an invalid token surfaces as the API's `401` in the tool result. An MCP session
is bound to the token that opened it before initialization completes. If the
adapter no longer retains a session's binding, the client must initialize a
new session with its own token.

1. Mint one token per agent with only the scopes it needs. Prefer a
   **restricted** token limited to the agent's own collections, e.g.
   `artifactctl token create my-agent --restricted --grant my-agent-work=collections:write`
   (add `collections:delete` only if it must delete). To a restricted token,
   every other collection does not exist. An unrestricted
   `--scope collections:write` token reaches every collection.
2. Put it in the agent's environment as `ARTIFACTSD_TOKEN` — never paste the
   value into a shared config file.
3. Print the client config with
   `artifactctl mcp config --client claude|codex|hermes --url <adapter-url>`:

```console
# Claude Code
$ claude mcp add --transport http artifactsd http://127.0.0.1:8081/ \
    --header "Authorization: Bearer $ARTIFACTSD_TOKEN"

# Codex CLI (~/.codex/config.toml)
[mcp_servers.artifactsd]
url = "http://127.0.0.1:8081/"
bearer_token_env_var = "ARTIFACTSD_TOKEN"

# Hermes Agent (~/.hermes/config.yaml; ARTIFACTSD_TOKEN in ~/.hermes/.env)
mcp_servers:
  artifactsd:
    url: http://127.0.0.1:8081/
    headers:
      Authorization: "Bearer ${ARTIFACTSD_TOKEN}"
```

The adapter serves no OAuth discovery documents (some clients ignore a
configured `Authorization` header when a server advertises OAuth).

It **listens on loopback (`127.0.0.1:8081`) by default**. Tokens travel to it in
clear text, so to reach it from other machines put a TLS-terminating proxy in
front and set `--listen` accordingly; the adapter logs a warning when it listens
beyond loopback. Its default port differs from `serve` (`:8080`) so both can run
on one host. See [Configuration → MCP adapter](configuration.md#mcp-adapter).

> **Upgrading:** `--token` / `ARTIFACTSD_MCP_TOKEN` were removed. The adapter
> refuses to start while either is set; unset it and configure your clients as
> above.

To run the adapter as a service, `artifactsd systemd --mcp [--user]
[--runtime local|docker|podman] [--install]` generates (and optionally
installs) a systemd unit for it — see
[Installation → systemd service](installation.md#systemd-service). Container
units publish the port on host `127.0.0.1` only.

## Tools

| Tool | Maps to | Notes |
|------|---------|-------|
| `artifact_create_collection` | `POST /collections` | name + policy flags; optional `expires_in` (e.g. `7d`) sets `expiry.expires_at` |
| `artifact_put_text` | `PUT .../files/{path}` | small text artifacts inline |
| `artifact_begin_upload` | `POST .../uploads` | returns a direct upload URL for large/binary |
| `artifact_get_metadata` | `GET ...?meta` (collection: `GET /collections/{c}/meta`) | own + effective + provenance; empty path = collection, trailing `/` = folder |
| `artifact_set_metadata` | `PATCH ...?meta` (collection: `PATCH /collections/{c}/meta`) | merge, same path convention |
| `artifact_delete` | `DELETE .../files/{path}` | needs a token with `collections:delete` |
| `artifact_list_tree` | `GET .../tree` | structured listing (unfiltered management view) |
| `artifact_get_public_url` | local compose | builds a `/c/...` URL |
| `artifact_create_share` | `POST .../shares` | returns a `/s/...` URL |
| `artifact_get_crawl_tree` | `GET .../crawl` | policy-filtered enumeration |
| `artifact_create_upload_request` | `POST /upload-requests` | returns a shareable `/r/...` upload URL; optional `file_metadata` |
| `artifact_list_upload_requests` | `GET /upload-requests` | all links + status |
| `artifact_get_upload_request` | `GET /upload-requests/{id}` | status, files uploaded |
| `artifact_revoke_upload_request` | `DELETE /upload-requests/{id}` | permanently close the link |

Tool inputs and outputs are JSON with explicit schemas, so agents get validation
and discoverability. API error codes are mapped to MCP tool errors with
actionable messages, and returned URLs are absolute (built from the server
`base_url`).

## Metadata documents

`artifact_set_metadata`'s `metadata` and `artifact_create_upload_request`'s
`file_metadata` take the same JSON metadata document used everywhere in the
[HTTP API](http-api.md): optional `access`, `content`, `crawl`, and `expiry`
sections. The adapter publishes the full field-by-field schema in the tool
definition and validates input against it, so a malformed patch (say, a
misspelled expiry field) is rejected with an error naming the offending
property before anything reaches the server.

Setting an expiry on a collection (omit `path` for the collection itself; end
it with `/` for a folder):

```json
{ "collection": "run-8f31c2",
  "metadata": { "expiry": { "expires_at": "2026-08-01T00:00:00Z" } } }
```

`expiry.expires_at` takes an absolute RFC3339 UTC timestamp;
`expiry.delete_after_creation` and `expiry.delete_after_last_access` take
durations like `7d`, `48h`, or `2w`. Any combination may be set — the earliest
resulting instant wins.

## Upload strategy

- **Small text** → `artifact_put_text` (the content is an inline string in the
  tool call).
- **Large or binary** → `artifact_begin_upload` returns an HTTP `upload_url`;
  the agent `PUT`s the bytes directly, out of band (setting the file's MIME
  type via the `Content-Type` request header), then optionally
  `artifact_create_share`. This keeps large binaries out of the MCP message
  channel.

## Example flow

```
1. artifact_create_collection("run-8f31c2")
2. artifact_put_text("run-8f31c2", "index.html", "<html>...</html>")
3. artifact_begin_upload("run-8f31c2", "report.pdf") -> upload_url
4. agent PUTs PDF bytes to upload_url with a "Content-Type: application/pdf" header
5. artifact_create_share("run-8f31c2", visibility="unlisted", expires_in="7d") -> /s/...
6. agent returns the share URL to the user
```
