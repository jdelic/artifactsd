# HTTP API reference

The HTTP management API under `/api/v1` is the **canonical interface** to
artifactsd; `artifactctl` and the MCP adapter are both clients of it. Public
file serving (`/c/...`, `/s/...`) and crawler endpoints are covered at the
[end of this page](#public-serving-endpoints).

## Conventions

- **Auth** — every `/api/v1` endpoint requires `Authorization: Bearer
  <api-token>` (or an authenticated admin session). Tokens carry scopes; a
  request needs the scope listed for its endpoint or it gets `403`.
- **Scopes** — `admin`, `collections:read`, `collections:write`,
  `shares:read`, `shares:write`, `tokens:read`, `tokens:write`. `admin` implies
  the rest.
- **Errors** — a consistent JSON envelope:
  ```json
  { "error": { "code": "not_found", "message": "collection does not exist" } }
  ```
  Codes: `bad_request`, `unauthorized`, `forbidden`, `not_found`, `conflict`,
  `payload_too_large`, `unsupported_media_type`, `rate_limited`, `internal`.
- **Timestamps** — RFC3339 UTC. **Durations** — `30d`, `48h`, `2w`.
- **Idempotency** — `PUT` of a file is a full, idempotent replace; `PATCH .../meta`
  merges.
- **Pagination** — list endpoints take `?limit=&cursor=` and return
  `{ "items": [...], "next_cursor": "..." }`.
- **Metadata** in request/response bodies is the JSON metadata document (access,
  content, crawl, expiry blocks).

## Collections

```
POST   /api/v1/collections                 create                 collections:write
GET    /api/v1/collections                 list (paginated)       collections:read
GET    /api/v1/collections/{c}             get one (+ metadata)   collections:read
PATCH  /api/v1/collections/{c}/meta        merge metadata         collections:write
DELETE /api/v1/collections/{c}             delete (cascades)      collections:write
```

Create body:

```json
{ "name": "sales-demo-2026",
  "metadata": {
    "access": { "visibility": "public", "directory_listing": true },
    "crawl":  { "crawlable": true, "indexable": false }
  } }
```

Returns `201` with the created collection, or `409 conflict` if the name exists.
`DELETE` cascades to the whole tree; pass `?force=` to delete a non-empty
collection.

## Files

```
PUT    /api/v1/collections/{c}/files/{path...}        upload/replace   collections:write
GET    /api/v1/collections/{c}/files/{path...}        download         collections:read
GET    /api/v1/collections/{c}/files/{path...}/meta   read metadata    collections:read
PATCH  /api/v1/collections/{c}/files/{path...}/meta   merge metadata   collections:write
DELETE /api/v1/collections/{c}/files/{path...}        delete           collections:write
```

**Upload (`PUT`)** — the body is the raw file bytes. The request `Content-Type`
sets `content.content_type` unless metadata overrides it. To attach a full
metadata document alongside a binary body, send it base64url-encoded in the
`Artifactsd-Metadata` header. The path is normalized and traversal-checked;
parent folders are implied. Oversized bodies get `413`. Response:

```json
{ "collection": "sales-demo-2026", "path": "report.pdf", "size": 18422,
  "content_type": "application/pdf", "checksum": "sha256:...",
  "url": "https://artifacts.example.com/c/sales-demo-2026/report.pdf",
  "updated_at": "2026-06-21T10:15:00Z" }
```

**Metadata (`GET .../meta`)** returns both the object's own metadata and the
resolved **effective** metadata, with secrets redacted to presence booleans:

```json
{ "path": "report.pdf",
  "own": { "content": { "...": "..." }, "access": { "...": "..." } },
  "effective": {
    "content": { "...": "..." },
    "access":  { "visibility": "protected", "auth": [ { "type": "bearer", "configured": true } ] },
    "crawl":   { "...": "..." },
    "expiry":  { "expires_at": "2026-07-01T00:00:00Z" } } }
```

`PATCH .../meta` takes a partial document and merges it. Setting
`access.auth[].token` or `password` in cleartext causes server-side hashing; the
cleartext is never stored.

## Folders

```
GET    /api/v1/collections/{c}/folders/{path...}/meta   read folder metadata     collections:read
PATCH  /api/v1/collections/{c}/folders/{path...}/meta   set/merge (materializes) collections:write
DELETE /api/v1/collections/{c}/folders/{path...}        delete subtree           collections:write
GET    /api/v1/collections/{c}/tree?path=&depth=        structured listing       collections:read
```

`tree` returns nodes with kind/size/content-type/expiry for the admin UI and the
CLI `tree` command. It is the management listing — distinct from the
policy-filtered crawl API below.

## Large uploads

For files too big to stream in one request, begin a session, then `PUT` the
bytes to the returned capability URL:

```
POST   /api/v1/collections/{c}/uploads     begin session → upload_url   collections:write
PUT    /api/v1/uploads/{upload_id}         stream bytes                 (no auth — see below)
GET    /api/v1/uploads/{upload_id}         session status               (no auth)
DELETE /api/v1/uploads/{upload_id}         abort session                (no auth)
```

Begin body and response:

```json
{ "path": "audio/summary.mp3",
  "metadata": { "content": { "content_type": "audio/mpeg" },
                "expiry":  { "delete_after_creation": "14d" } } }
```
```json
{ "upload_url": "https://artifacts.example.com/api/v1/uploads/upl_8f31c2",
  "upload_id": "upl_8f31c2", "method": "PUT",
  "expires_at": "2026-06-21T11:00:00Z" }
```

The `upload_url` carries an unguessable `upload_id` and needs **no additional
auth** — possession is the capability, so an agent can be handed the URL and
`PUT` directly. Sessions are single-use with a short TTL (`ARTIFACTSD_UPLOAD_TTL`);
unfinished ones are reaped by the janitor.

## Shares

```
POST   /api/v1/collections/{c}/shares      create share          shares:write
GET    /api/v1/collections/{c}/shares      list shares           shares:read
GET    /api/v1/shares/{share_id}           get share             shares:read
PATCH  /api/v1/shares/{share_id}           update policy/expiry  shares:write
DELETE /api/v1/shares/{share_id}           revoke                shares:write
```

Create body — `target_path: ""` shares the whole collection:

```json
{ "target_path": "report.pdf",
  "metadata": {
    "access": { "visibility": "unlisted", "directory_listing": false,
                "auth": [ { "type": "bearer", "token": "demo-token" } ] },
    "expiry": { "expires_at": "2026-07-01T00:00:00Z" } } }
```

The response includes the share URL `https://.../s/{share_id}/`.

## Crawl API

```
GET /api/v1/crawl                                 all crawlable collections   collections:read
GET /api/v1/collections/{c}/crawl?path=&depth=    crawl tree for one          collections:read
```

Returns a structured, **policy-filtered** listing for agents (so they don't
scrape HTML). Honors `crawl.crawlable`: non-crawlable objects are omitted unless
the token carries `admin` scope.

## Tokens, audit & maintenance (admin-scoped)

```
GET    /api/v1/tokens                  list API tokens (metadata only)   admin
POST   /api/v1/tokens                  create token → secret shown ONCE  admin
DELETE /api/v1/tokens/{id}             revoke token                      admin
GET    /api/v1/audit?since=&action=    audit log                         admin
GET    /api/v1/stats                   counts and storage usage          admin
POST   /api/v1/maintenance/cleanup     run a janitor pass now            admin
```

The created token secret is returned **once** at creation and never again.
The audit log records deletions and admin actions (and authenticated access to
protected objects). There is no access-log endpoint — access logs stream to
stdout. OIDC and admin auth are configured via [environment](configuration.md),
not management endpoints.

## Status codes

| Situation | Code |
|-----------|------|
| Success (read/replace) | `200` |
| Created | `201` |
| Bad input / unsafe header / bad path | `400` |
| Missing or invalid API token | `401` |
| Valid token, insufficient scope | `403` |
| Unknown object, or private/expired on a public route | `404` |
| Name or share conflict | `409` |
| Over `max_upload_size` | `413` |
| Auth-failure rate limit tripped | `429` |

## Public serving endpoints

These are unauthenticated by default (policy is enforced per object) and are not
under `/api/v1`:

```
GET|HEAD /c/{collection}/{path...}    serve a collection file (or directory listing)
GET|HEAD /s/{share_id}/{path...}      serve via a share URL
GET|HEAD /robots.txt                  generated crawl policy
GET|HEAD /sitemap.xml                 generated sitemap (public, indexable URLs only)
GET      /healthz, /readyz            liveness / readiness probes
```

Protected and private objects, and expired ones, return `404` on these routes so
their existence isn't leaked. See [`.agent-docs/design/05-serving-crawl.md`](../.agent-docs/design/05-serving-crawl.md)
for the serving and crawl details.
