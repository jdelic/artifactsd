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
  `shares:read`, `shares:write`. `admin` implies the rest, and is the only
  scope for tokens, upload requests, audit, stats, and maintenance;
  `<resource>:write` implies `<resource>:read`.
- **Errors** — a consistent JSON envelope:
  ```json
  { "error": { "code": "not_found", "message": "collection does not exist" } }
  ```
  Codes: `bad_request`, `unauthorized`, `forbidden`, `not_found`, `conflict`,
  `payload_too_large`, `unsupported_media_type`, `rate_limited`, `internal`.
- **Timestamps** — RFC3339 UTC. **Durations** — `30d`, `48h`, `2w`.
- **Idempotency** — `PUT` of a file is a full, idempotent replace; `PATCH .../meta`
  merges.
- **Pagination** — `GET /collections` takes `?limit=&cursor=` and returns
  `{ "items": [...], "next_cursor": "..." }`. The other list endpoints use the
  same `items` envelope but are unpaginated (their result sets stay small at
  artifactsd's intended scale).
- **Tree depth** — `?depth=` (on `tree` and `crawl`) omitted or `0` returns
  the full tree; a positive value limits the number of levels below the base
  path; anything else is a `400`.
- **Metadata** in request/response bodies is the JSON metadata document (access,
  content, crawl, expiry blocks).

## Collections

```
POST   /api/v1/collections                 create                 collections:write
GET    /api/v1/collections                 list (paginated)       collections:read
GET    /api/v1/collections/{c}             get one (+ metadata)   collections:read
GET    /api/v1/collections/{c}/meta        own + effective meta   collections:read
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

**Metadata (`GET .../meta`)** — available at all three levels (collection,
folder, file) in one shape: the object's own metadata, the resolved
**effective** metadata (flat view, secrets redacted to presence booleans), and
provenance — `chain` (the inheritance layers, lowest precedence first) and
`sources` (which layer determined each effective property; headers per-key).
This backs `artifactctl meta explain`:

```json
{ "path": "report.pdf",
  "own": { "access": { "visibility": "protected" } },
  "effective": {
    "visibility": "protected", "directory_listing": false,
    "content_type": "application/pdf",
    "headers": { "Cache-Control": "no-store" },
    "sandbox": true, "crawlable": false, "indexable": false,
    "auth": [ { "type": "bearer", "configured": true } ],
    "expires_at": "2026-07-01T00:00:00Z" },
  "chain": [
    { "scope": "defaults",   "meta": { "...": "..." } },
    { "scope": "collection", "meta": { "...": "..." } },
    { "scope": "file", "path": "report.pdf", "meta": { "...": "..." } } ],
  "sources": { "visibility": "file report.pdf",
               "headers.Cache-Control": "file report.pdf",
               "crawlable": "defaults" } }
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

Deletes are kind-checked in both directions: `DELETE .../files/{path}` on a
folder and `DELETE .../folders/{path}` on a file each return `409` — recursive
subtree deletion only happens through the folders route.

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

## Upload request links

Shareable capability URLs (`/r/{request_id}`) that let third parties — humans
via a browser form, or agents programmatically — upload files into a
designated collection/folder **without any API credentials**. Distinct from
upload sessions above: persistent, multi-file, multi-submitter. Management is
admin-scoped:

```
POST   /api/v1/upload-requests                          create                admin
GET    /api/v1/upload-requests                          list                  admin
GET    /api/v1/upload-requests/{id}                     get one               admin
PATCH  /api/v1/upload-requests/{id}                     update label/expiry/  admin
                                                        status (e.g. closed)
DELETE /api/v1/upload-requests/{id}                     revoke                admin
GET    /api/v1/upload-requests/{id}/submissions         list submissions      admin
```

Create body (all fields except `collection` optional):

```json
{ "collection": "inbox", "target_folder": "q2-deliverables",
  "label": "Q2 Deliverables", "description": "PDF or DOCX please.",
  "file_metadata": { "access": { "visibility": "private" } },
  "allow_uploader_access": false, "single_use": false,
  "use_submission_folders": true,
  "max_files": 50, "max_file_size": 52428800,
  "expires_at": "2026-08-01T00:00:00Z" }
```

The response includes the shareable `url` (`https://.../r/{request_id}`).
`file_metadata` is applied to every uploaded file; `use_submission_folders`
nests each submission under `{target_folder}/{submission_id}/`. **Closing**
(`PATCH {"status":"closed"}`) stops new uploads and keeps the files;
**revoking** (`DELETE`) is the permanent form and also escalates an
already-closed link. Neither deletes uploaded files.

Public routes (no auth — possession of the URL is the capability):

```
GET  /r/{request_id}           upload form (HTML) or status (JSON clients)
POST /r/{request_id}           accept upload(s): multipart file parts, or a raw body
GET  /r/{request_id}/status    JSON status: active/closed/expired, counts
```

Status codes: `200` upload accepted · `404` unknown link · `410` expired,
closed, or revoked · `409` max-files quota reached · `413` over the per-link
or server size limit.

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
GET|POST /r/{request_id}              upload request link (form / accept uploads)
GET|HEAD /robots.txt                  generated crawl policy
GET|HEAD /sitemap.xml                 generated sitemap (public, indexable URLs only)
GET      /healthz, /readyz            liveness / readiness probes
```

Protected and private objects, and expired ones, return `404` on these routes so
their existence isn't leaked. See [`.agent-docs/design/05-serving-crawl.md`](../.agent-docs/design/05-serving-crawl.md)
for the serving and crawl details.
