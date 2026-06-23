# 04 — HTTP management API

Base: `/api/v1`. All management endpoints require authentication
([06](06-auth-security.md)) via an API token (`Authorization: Bearer tok_...`)
or an authenticated admin session. Responses are JSON; errors use a consistent
envelope.

## 1. Conventions

- **Auth**: `Authorization: Bearer <api-token>`. Tokens carry scopes
  (`admin`, `collections:read`, `collections:write`, `shares:write`, ...).
- **Errors**:
  ```json
  { "error": { "code": "not_found", "message": "collection does not exist" } }
  ```
  Codes: `bad_request`, `unauthorized`, `forbidden`, `not_found`, `conflict`,
  `payload_too_large`, `unsupported_media_type`, `rate_limited`, `internal`.
- **Timestamps**: RFC3339 UTC. **Durations**: `30d`, `48h`, `2w`.
- **Metadata** in bodies is the JSON form of the [metadata document](03-metadata-policy.md).
- **Idempotency**: `PUT` file is idempotent (full replace). `PATCH` meta merges.
- **Pagination**: list endpoints accept `?limit=&cursor=`; return
  `{ "items": [...], "next_cursor": "..." }`.

## 2. Collections (SPEC §12.1)

```
POST   /api/v1/collections                 create
GET    /api/v1/collections                 list (paginated)
GET    /api/v1/collections/{c}             get one (incl. metadata)
PATCH  /api/v1/collections/{c}/meta        merge metadata
DELETE /api/v1/collections/{c}             delete (cascades; ?force= for non-empty)
```

Create body:
```json
{ "name": "sales-demo-2026",
  "metadata": { "access": {"visibility":"public","directory_listing":true},
                "crawl": {"crawlable":true,"indexable":false} } }
```
`201` with the created collection; `409 conflict` if the name exists.

## 3. Files (SPEC §12.2)

```
PUT    /api/v1/collections/{c}/files/{path...}        upload/replace (body = bytes)
GET    /api/v1/collections/{c}/files/{path...}        download (management, bypasses public policy with proper scope)
GET    /api/v1/collections/{c}/files/{path...}/meta   read metadata (effective + own)
PATCH  /api/v1/collections/{c}/files/{path...}/meta   merge metadata
DELETE /api/v1/collections/{c}/files/{path...}        delete file
```

Upload:
- Body is the raw file bytes; `Content-Type` header sets `content.content_type`
  unless overridden by metadata.
- Optional `Artifactsd-Metadata: <base64url(JSON)>` header carries a metadata
  document alongside a binary body (SPEC §12.2).
- Enforces `limits.max_upload_size`; `413` if exceeded.
- `path` is normalized & traversal-checked; parent folders are implied.
- Response `201`/`200`:
  ```json
  { "collection":"sales-demo-2026","path":"report.pdf","size":18422,
    "content_type":"application/pdf","checksum":"sha256:...",
    "url":"https://.../c/sales-demo-2026/report.pdf",
    "updated_at":"2026-06-21T10:15:00Z" }
  ```

`GET .../meta` returns both the object's own metadata and the resolved
**effective** metadata (with secrets redacted to hash-presence booleans):
```json
{ "path":"report.pdf",
  "own":   { "content": {...}, "access": {...} },
  "effective": { "content": {...}, "access": {"visibility":"protected","auth":[{"type":"bearer","configured":true}]},
                 "crawl": {...}, "expiry": {"expires_at":"2026-07-01T00:00:00Z"} } }
```

`PATCH .../meta` accepts a partial document; merge rules per
[03 §2](03-metadata-policy.md). Setting `access.auth[].token`/`password`
(cleartext) causes hashing server-side; the cleartext is never stored.

## 4. Folders

```
GET    /api/v1/collections/{c}/folders/{path...}/meta   read folder metadata
PATCH  /api/v1/collections/{c}/folders/{path...}/meta   set/merge folder metadata (materializes the folder)
DELETE /api/v1/collections/{c}/folders/{path...}        delete folder subtree
GET    /api/v1/collections/{c}/tree?path=&depth=        structured listing (management)
```

`tree` returns nodes with kind/size/content-type/expiry for the admin UI and CLI
`tree` command. Distinct from the public crawl API ([05](05-serving-crawl.md)),
which is policy-filtered.

## 5. Large uploads (SPEC §12.3)

```
POST   /api/v1/collections/{c}/uploads     begin session → upload_url
PUT    /api/v1/uploads/{upload_id}         stream bytes (no auth: upload_id is the capability)
GET    /api/v1/uploads/{upload_id}         session status
DELETE /api/v1/uploads/{upload_id}         abort session
```

Begin body:
```json
{ "path":"audio/summary.mp3",
  "metadata": { "content": {"content_type":"audio/mpeg"},
                "expiry": {"delete_after_creation":"14d"} } }
```
Response:
```json
{ "upload_url":"https://.../api/v1/uploads/upl_8f31c2",
  "upload_id":"upl_8f31c2","method":"PUT","expires_at":"2026-06-21T11:00:00Z" }
```
The `upload_url` carries an unguessable `upload_id` and needs **no additional
auth** — possession is the capability (decision D2), so an agent can be handed
the URL and PUT directly. The `PUT` streams to a temp blob; on success the node
is committed and the session marked `completed`. Sessions are single-use with a
short TTL; unfinished ones expire and are reaped ([08](08-expiry-janitor.md)).
See [07](07-storage-uploads.md) and [14 §5](14-access-control.md).

## 6. Shares (SPEC §5.2, §20.2)

```
POST   /api/v1/collections/{c}/shares      create share for collection/folder/file
GET    /api/v1/collections/{c}/shares      list shares
GET    /api/v1/shares/{share_id}           get share (admin)
PATCH  /api/v1/shares/{share_id}           update share policy/expiry
DELETE /api/v1/shares/{share_id}           revoke share
```

Create body:
```json
{ "target_path":"report.pdf",
  "metadata": { "access": {"visibility":"unlisted","directory_listing":false,
                           "auth":[{"type":"bearer","token":"demo-token"}]},
                "expiry": {"expires_at":"2026-07-01T00:00:00Z"} } }
```
Response includes the share URL `https://.../s/{share_id}/`. `target_path:""`
shares the whole collection.

## 7. Crawl API (SPEC §10.5)

```
GET /api/v1/crawl                              all crawlable collections (token-scoped)
GET /api/v1/collections/{c}/crawl?path=&depth= crawl tree for one collection
```
Returns the structured, policy-filtered listing in SPEC §10.5. Honors
`crawl.crawlable`: non-crawlable objects are omitted unless the token has
`admin` scope. Detailed in [05](05-serving-crawl.md).

## 8. Tokens & settings (admin-scoped)

```
GET    /api/v1/tokens                  list API tokens (metadata only)
POST   /api/v1/tokens                  create token → returns secret ONCE
DELETE /api/v1/tokens/{id}             revoke token
GET    /api/v1/audit?since=&action=    audit log (deletions + admin actions)
POST   /api/v1/maintenance/cleanup     trigger janitor pass now (SPEC §14)
GET    /api/v1/stats                   counts, storage usage
```

There is no access-log endpoint: access logs stream to stdout (decision F5).
OIDC and admin auth are configured via environment ([12](12-config.md),
[14](14-access-control.md)), not management endpoints.

## 9. Status codes summary

| Situation | Code |
|-----------|------|
| Success (read/replace) | 200 |
| Created | 201 |
| Bad input / unsafe header / bad path | 400 |
| Missing/invalid API token | 401 |
| Valid token, insufficient scope | 403 |
| Unknown collection/file/share, or private/expired on public routes | 404 |
| Name/share conflict | 409 |
| Over `max_upload_size` | 413 |
| Auth-failure rate limit tripped | 429 |
