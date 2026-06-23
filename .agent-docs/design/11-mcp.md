# 11 — MCP adapter

Implements SPEC §16. The MCP adapter exposes agent-friendly tools over the HTTP
API. **MCP is an adapter, not the canonical storage layer** (SPEC §3, §16).

## 1. Shape

- A subcommand `artifactd mcp` (and/or a standalone build) runs an MCP server
  over stdio (default) or HTTP/SSE.
- Tools call the artifactd HTTP API via `pkg/apiclient` — the same client the
  CLI uses. The adapter holds a configured `base_url` + API token (from env/
  config), so agents don't handle artifactd auth directly.
- SDK: official `github.com/modelcontextprotocol/go-sdk` (pending confirmation
  in OPEN-QUESTIONS; fallback `mark3labs/mcp-go`).

## 2. Tools (SPEC §16)

| Tool | Maps to | Notes |
|------|---------|-------|
| `artifact_create_collection` | `POST /collections` | name + policy flags |
| `artifact_put_text` | `PUT .../files/{path}` | small text artifacts inline |
| `artifact_begin_upload` | `POST .../uploads` | returns direct upload URL for large/binary |
| `artifact_get_metadata` | `GET .../meta` | effective + own |
| `artifact_set_metadata` | `PATCH .../meta` | merge |
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
- Errors map artifactd API error codes → MCP tool errors with actionable
  messages.
- Each tool documents required scopes; the adapter's token must carry them.
- URLs returned are absolute (using server `base_url`).

## 6. Scope

Minimal MCP adapter is in MVP (SPEC §21): the ten tools above over stdio. SSE/
HTTP transport and richer tools (folder upload, bulk ops) are post-MVP.
