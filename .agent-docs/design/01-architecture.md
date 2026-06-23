# 01 — Architecture

## 0. Scale & intent (decision N7)

artifactsd is **self-contained, easy to configure, and easy to deploy**. The
expected audience for any published artifact is **up to ~50 viewers** — this is
easy artifact storage for agents and humans, not a mass-distribution platform.
It runs as a **single process** that owns its local state (SQLite + blobs, a
portable data dir per B1). If an artifact must reach hundreds/thousands/millions
of users, the operator/agent should front it with S3 or a CDN instead. These
assumptions justify several choices below: in-process SQLite, an in-memory rate
limiter, a single-process janitor, and local-filesystem blobs.

## 1. Component map

`artifactsd` is a single Go binary that runs several cooperating subsystems in
one process. The CLI (`artifactctl`) and MCP adapter are clients of the HTTP
API; the HTTP API is the canonical interface (SPEC §3).

```
                         ┌──────────────────────────────────────────────┐
                         │                 artifactsd                     │
   public clients ──────▶│  ┌───────────────┐   ┌────────────────────┐  │
   (browsers, agents)    │  │ Public serving │   │ Management API     │  │
                         │  │  /c /s /robots │   │  /api/v1/...        │  │
                         │  │  /sitemap.xml  │   │                    │  │
                         │  └──────┬─────────┘   └─────────┬──────────┘  │
   admin browser ───────▶│  ┌──────┴───────────────────────┴─────────┐  │
                         │  │            HTTP server (mux)            │  │
                         │  │   middleware: logging, auth, limits     │  │
                         │  └──────┬───────────────────────┬─────────┘  │
                         │  ┌──────┴─────────┐   ┌──────────┴────────┐   │
                         │  │ Admin UI       │   │ Policy / metadata │   │
                         │  │ /admin         │   │ resolution engine │   │
                         │  └────────────────┘   └──────────┬────────┘   │
                         │  ┌──────────────┐   ┌────────────┴────────┐   │
                         │  │ Expiry        │   │ Storage layer      │   │
                         │  │ janitor (goroutine)│  blobstore + DB   │   │
                         │  └──────────────┘   └─────────┬──────────┘   │
                         └─────────────────────────────────┼────────────┘
                                                           │
                                  ┌────────────────────────┴───────────┐
                                  │ SQLite (metadata)  + FS blobs       │
                                  └─────────────────────────────────────┘

  artifactctl ── HTTP ─▶ Management API
  MCP adapter  ── (in-process or HTTP) ─▶ Management API
```

## 2. Process model

A single `artifactsd serve` process hosts:

1. **HTTP listener(s)** — one `http.Server` bound to `server.listen`. TLS is
   terminated in-process when `server.tls` is configured; otherwise plain HTTP
   (for use behind a reverse proxy). All route groups share one mux.
2. **Expiry janitor** — a background goroutine on a ticker (default 60s) that
   deletes expired objects and writes audit records. See [08](08-expiry-janitor.md).
3. **Upload-session reaper** — sweeps abandoned/expired upload sessions (folded
   into the janitor loop).

Graceful shutdown: on SIGINT/SIGTERM, stop accepting new connections, drain
in-flight requests with a timeout, stop the janitor, close the DB.

## 3. Route groups

| Prefix | Handler group | Auth | Doc |
|--------|---------------|------|-----|
| `/c/{collection}/...` | Public/human serving | per-object policy | [05](05-serving-crawl.md) |
| `/s/{share_id}/...` | Share serving | per-share policy | [05](05-serving-crawl.md) |
| `/robots.txt`, `/c/{c}/robots.txt` | Robots policy | public | [05](05-serving-crawl.md) |
| `/sitemap.xml`, `/c/{c}/sitemap.xml` | Sitemaps | public | [05](05-serving-crawl.md) |
| `/api/v1/...` | Management API | API token / admin | [04](04-http-api.md) |
| `/api/v1/uploads/{id}` | Direct upload sink | upload-session token | [07](07-storage-uploads.md) |
| `/admin/...` | Admin web UI | OIDC group-gated (+ basic fallback) | [09](09-admin-ui.md), [14](14-access-control.md) |
| `/auth/oidc/...` | OIDC login/callback | public (initiates auth) | [14](14-access-control.md) |
| `/healthz`, `/readyz` | Liveness/readiness | public | this doc |

Routing uses Go 1.22 `http.ServeMux` method+path patterns
(`mux.HandleFunc("GET /c/{collection}/", ...)`), with a custom matcher for the
wildcard tail path where needed.

## 4. Internal package layout (proposed)

```
github.com/jdelic/artifactsd
├── cmd/
│   ├── artifactsd/        # server entrypoint (serve, migrate, version)
│   └── artifactctl/      # CLI entrypoint (cobra)
├── internal/
│   ├── config/           # config load/validate, defaults
│   ├── store/            # SQLite access, migrations (embed), models
│   ├── blob/             # filesystem blob store
│   ├── meta/             # metadata document type + resolution/merge engine
│   ├── policy/           # access decisions (visibility, auth eval)
│   ├── auth/             # bearer/basic/oidc verifiers, hashing
│   ├── server/           # http.Server wiring, middleware, mux
│   │   ├── serve/        # public /c /s handlers
│   │   ├── api/          # /api/v1 handlers
│   │   ├── admin/        # /admin handlers + templates (embed)
│   │   ├── crawl/        # robots, sitemap, crawl API
│   │   └── middleware/   # logging, recovery, ratelimit, auth ctx
│   ├── janitor/          # expiry + upload reaper
│   ├── upload/           # upload session lifecycle
│   ├── audit/            # audit log (DB) + access log (stdout) emitters
│   └── mcp/              # MCP adapter (server + tools)
├── pkg/
│   └── apiclient/        # Go client for the HTTP API (used by CLI + MCP)
└── .agent-docs/
```

`internal/` keeps the surface private; `pkg/apiclient` is the one shared,
importable client so the CLI and MCP adapter never duplicate request logic.

## 5. Data flow examples

**Public file fetch** `GET /c/demo/report.pdf`:
1. mux → serve handler resolves `(collection=demo, path=report.pdf)`.
2. store: load collection + node; 404 if absent or expired.
3. meta: resolve effective metadata (config defaults → collection → folders →
   file). See [03](03-metadata-policy.md).
4. policy: evaluate visibility + auth against the request. 401/403/404 per rules.
5. serve: set Content-Type, safe headers, crawler headers; stream blob;
   update last-access (throttled).

**Direct small upload** `PUT /api/v1/collections/demo/files/index.html`:
1. API auth middleware authorizes the management caller.
2. Validate/normalize path; enforce size limit; decode optional
   `Artifactsd-Metadata` header.
3. blob: write bytes to a temp file, fsync, atomic rename into place.
4. store: upsert node row + metadata in one transaction.
5. audit: record the action.

## 6. Technology choices

See the decision table in [README](README.md). Key constraints driving them:
single self-contained binary (→ pure-Go SQLite, `embed.FS` for assets and
migrations, no external services), and "don't over-engineer" (→ stdlib HTTP and
`slog`, cobra only because the CLI genuinely has nested subcommands).

## 7. Observability

- **Access logs → stdout** as structured events (method, path, status, bytes,
  duration, auth outcome, collection); treated as an event stream, not stored
  (decision F5, 12factor).
- **App logs + errors → stderr** via `log/slog` (`--log-level` controls).
- **Audit log** persisted in the `audit_logs` DB table (admin actions +
  deletions, SPEC §17) and mirrored to stderr; backs the admin "deletion log"
  view. There is no DB-backed access-log view.
- `/healthz` (process up) and `/readyz` (DB reachable, data dir writable).

## 8. Concurrency & integrity

- SQLite opened in WAL mode, `busy_timeout` set, a single `*sql.DB` pool.
  Writes are serialized by SQLite; keep transactions short.
- Blob writes are temp-file + atomic rename; deletes remove blob then row (or
  row then blob) within a recoverable order, with the janitor reconciling
  orphans.
- A future move to S3/object storage (SPEC §2 non-goal now) is kept possible by
  hiding storage behind the `blob` package interface.
