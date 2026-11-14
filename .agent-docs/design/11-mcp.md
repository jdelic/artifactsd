# 11 — MCP adapter

Implements SPEC §16. The MCP adapter exposes agent-friendly tools over the HTTP
API. **MCP is an adapter, not the canonical storage layer** (SPEC §3, §16).

## 1. Shape

- A subcommand `artifactsd mcp` runs an MCP server over **Streamable HTTP** —
  the single committed transport (decisions E4/N1); we do **not** ship stdio.
  It listens on loopback (`127.0.0.1:8081`) by default; reaching it from other
  hosts requires a TLS-terminating proxy in front (it warns when listening
  beyond loopback).
- **Pass-through tokens** (artifactsd-m9e.18, owner decision 2026-11-13). The
  adapter holds no credentials. Every MCP request must carry
  `Authorization: Bearer <artifactsd API token>` (else `401`,
  `WWW-Authenticate: Bearer realm="artifactsd-mcp"`, no `resource_metadata`, and
  `/.well-known/oauth-*` answer 404 so clients don't switch to OAuth). The SDK's
  per-session server factory builds an `apiclient.Client` with the token of the
  initializing request, so tools act with exactly that token's scopes; the API
  validates it. Each `Mcp-Session-Id` is bound to the hash of its token — a
  request on that session with another token gets 401. The old `--token` /
  `ARTIFACTSD_MCP_TOKEN` is a startup error. `artifactctl mcp config --client
  claude|codex|hermes` prints client snippets reading `$ARTIFACTSD_TOKEN`.
- Tools call the artifactsd HTTP API via `pkg/apiclient` — the same client the
  CLI uses.
- SDK: no operator preference (decision E3); use official
  `github.com/modelcontextprotocol/go-sdk` if stable at build time, else
  `mark3labs/mcp-go`.

## 2. Tools (SPEC §16)

| Tool | Maps to | Notes |
|------|---------|-------|
| `artifact_create_collection` | `POST /collections` | name + policy flags |
| `artifact_put_text` | `PUT .../files/{path}` | small text artifacts inline |
| `artifact_begin_upload` | `POST .../uploads` | returns direct upload URL for large/binary |
| `artifact_get_metadata` | `GET ...?meta` (collection: `/collections/{c}/meta`) | effective + own |
| `artifact_set_metadata` | `PATCH ...?meta` (collection: `/collections/{c}/meta`) | merge |
| `artifact_delete` | `DELETE .../files/{path}` | |
| `artifact_list_tree` | `GET .../tree` or `/crawl` | structured listing |
| `artifact_get_public_url` | local compose | builds `/c/...` URL |
| `artifact_create_share` | `POST .../shares` | returns `/s/...` URL |
| `artifact_get_crawl_tree` | `GET .../crawl` | policy-filtered enumeration |

## 3. Upload strategy (SPEC §16)

- Small text → `artifact_put_text` (inline string in the tool call).
- Large/binary → `artifact_begin_upload` returns an HTTP `upload_url`; the agent
  PUTs bytes directly (out of band), then optionally `artifact_create_share`.
  This keeps large binaries out of the MCP message channel (SPEC §16 example
  flow).

## 4. Example flow (SPEC §16)

```
1. artifact_create_collection("run-8f31c2")
2. artifact_put_text("run-8f31c2","index.html","<html>...</html>")
3. artifact_begin_upload("run-8f31c2","report.pdf","application/pdf") → upload_url
4. agent PUTs PDF bytes to upload_url
5. artifact_create_share("run-8f31c2", visibility="unlisted", expires_in="7d") → /s/...
6. agent returns the share URL to the user
```

## 5. Tool I/O conventions

- Inputs/outputs are JSON with explicit schemas (MCP tool schemas) so agents get
  validation and discoverability.
- Errors map artifactsd API error codes → MCP tool errors with actionable
  messages.
- Each tool documents required scopes; the adapter's token must carry them.
- URLs returned are absolute (using server `base_url`).

## 6. Scope

Minimal MCP adapter is in MVP (SPEC §21): the ten tools above over the single
Streamable HTTP transport. Richer tools (folder upload, bulk ops) are post-MVP.
