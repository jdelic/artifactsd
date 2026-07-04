# 15 — Upload Request Links

## 1. Concept

An **upload request link** is a shareable, capability-style URL that lets a
third party (a human or an AI agent) upload one or more files to a designated
location in artifactsd — without needing an API token or admin credentials.
The link owner (an admin) configures where files land, what metadata they carry,
and whether the uploader is shown their file URL(s) after upload.

This enables workflows like:

- *"Send me your deliverable"* — share the link with a client or contractor who
  uploads a PDF/ZIP; the admin reviews it in the collection browser.
- *"Agent, upload your build artifacts here"* — hand an upload request URL to an
  autonomous agent that POSTs files programmatically.
- *"Public submissions inbox"* — an always-open link embedded in a form or docs
  page where multiple contributors upload files.

Upload request links are distinct from the existing **upload sessions**
([07 §3](07-storage-uploads.md)):

| | Upload session (`/api/v1/uploads/{id}`) | Upload request link (`/r/{id}`) |
|--|--|--|
| Created by | API caller with management token | Admin (API / CLI / admin UI) |
| Lifespan | Single-use, short TTL (default 1h) | Persistent; configured expiry |
| Files | One file per session | Many files, many submitters |
| Auth to create | `collections:write` scope | Admin scope |
| UX | API/programmatic only | Browser form **and** API |

## 2. URL pattern

```
/r/{request_id}
```

`r` follows the same one-letter prefix convention as `/c/` (collections) and
`/s/` (shares). `request_id` is ≥ 16 base62 characters (~95 bits, CSPRNG) —
same entropy as share IDs.

### Routes

| Method | Path | Auth | Action |
|--------|------|------|--------|
| `GET` | `/r/{request_id}` | none | Serve the upload form (HTML) or status JSON for API clients |
| `POST` | `/r/{request_id}` | none | Accept file upload(s); possession of the URL is the capability |
| `GET` | `/r/{request_id}/status` | none | JSON status: active/closed/expired, label, upload count |

## 3. Data model

### `upload_requests` table (migration `0003_upload_requests`)

```sql
CREATE TABLE upload_requests (
    id                    INTEGER PRIMARY KEY,
    request_id            TEXT NOT NULL UNIQUE,       -- ≥16 base62 chars (~95 bits)
    collection_id         INTEGER NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
    target_folder         TEXT NOT NULL DEFAULT '',   -- '' = root of collection
    label                 TEXT,                       -- shown on the upload form
    description           TEXT,                       -- longer text on the upload form
    file_metadata         TEXT NOT NULL DEFAULT '{}', -- metadata applied to each uploaded file (JSON)
    allow_uploader_access BOOLEAN NOT NULL DEFAULT 0, -- show file URLs to uploader on success
    use_submission_folders BOOLEAN NOT NULL DEFAULT 0,-- if true, nest files under {target_folder}/{submission_id}/
    single_use            BOOLEAN NOT NULL DEFAULT 0, -- if true, auto-close after first successful submission
    max_files             INTEGER,                    -- optional cap on total files (across all submitters)
    max_file_size         INTEGER,                    -- optional per-file size override (≤ server max_upload_size)
    files_uploaded        INTEGER NOT NULL DEFAULT 0, -- running counter
    status                TEXT NOT NULL DEFAULT 'active', -- active|closed|revoked|expired
    created_at            TEXT NOT NULL,
    expires_at            TEXT,                       -- NULL = no expiry
    revoked_at            TEXT,
    last_used_at          TEXT
);
CREATE INDEX idx_upload_requests_status ON upload_requests(status, expires_at);
```

### `upload_request_submissions` table

Each POST to `/r/{request_id}` that lands one or more files creates one
submission row. This lets the admin see "N files from K submitters" and, when
`use_submission_folders` is enabled, reconstructs which files belong together.

```sql
CREATE TABLE upload_request_submissions (
    id                    INTEGER PRIMARY KEY,
    upload_request_id     INTEGER NOT NULL REFERENCES upload_requests(id) ON DELETE CASCADE,
    submission_id         TEXT NOT NULL UNIQUE,   -- 8 base62 chars; also the folder name when use_submission_folders=true
    file_count            INTEGER NOT NULL DEFAULT 0,
    total_bytes           INTEGER NOT NULL DEFAULT 0,
    client_ip             TEXT,
    created_at            TEXT NOT NULL
);
CREATE INDEX idx_submissions_request ON upload_request_submissions(upload_request_id);
```

### File placement

With `use_submission_folders = false` (default):

```
{collection}/{target_folder}/{filename}
```

If a file with the same name already exists, it is **overwritten** (consistent
with the PUT-file semantics in the management API). This is the right default for
agent workflows where filenames are unique by convention.

With `use_submission_folders = true`:

```
{collection}/{target_folder}/{submission_id}/{filename}
```

Each POST generates a new `submission_id`, preventing collision between
concurrent uploaders of identically-named files. Use this for multi-human
submission scenarios (e.g. client deliverables inbox).

## 4. Public upload interface

### Browser flow

`GET /r/{request_id}` returns an HTML page (server-rendered, embedded template,
consistent with the admin UI approach in [09](09-admin-ui.md)):

- Title: label if set, else "Upload files"
- Optional description text
- File picker with multi-file support + drag-and-drop
- Size hint if `max_file_size` is set
- Upload button; htmx `POST /r/{request_id}` with `multipart/form-data`

On success, the page shows:
- "Files uploaded successfully"
- If `allow_uploader_access = true` AND effective visibility is `public` or
  `unlisted`: direct URLs for each uploaded file (copy-able links)

On error (link expired, closed, size exceeded, max-files reached): clear error
message with the specific reason.

### API / agent flow

An agent POSTs `multipart/form-data` or a plain file body to
`POST /r/{request_id}`. Accepts both:

- `multipart/form-data` with one or more `file` parts (browser-native)
- `Content-Type: <mime>` raw body for a single file; `Content-Disposition`
  header provides the filename (optional; server generates one if absent)

Response (`application/json`):

```json
{
  "submission_id": "a7kZ9pLm",
  "files": [
    {
      "filename": "report.pdf",
      "path": "uploads/a7kZ9pLm/report.pdf",
      "size": 184221,
      "url": "https://artifacts.example.com/c/inbox/uploads/a7kZ9pLm/report.pdf"
    }
  ],
  "uploader_access": true
}
```

`url` is only present when `allow_uploader_access = true` and visibility allows
unauthenticated access (`public` or `unlisted`).

### Status codes

| Situation | Code |
|-----------|------|
| Successful upload | 200 |
| Link not found | 404 |
| Link expired, closed, or revoked | 410 Gone |
| Max-files quota reached | 409 Conflict |
| File over `max_file_size` | 413 Payload Too Large |
| Over server `max_upload_size` | 413 Payload Too Large |

## 5. Management API

All endpoints require `admin` scope (or `upload-requests:write` for
create/revoke, TBD — defer scope granularity to a future task if not needed at
M2 launch).

```
POST   /api/v1/upload-requests              create
GET    /api/v1/upload-requests              list (paginated)
GET    /api/v1/upload-requests/{id}         get one
PATCH  /api/v1/upload-requests/{id}         update (label/description/expiry/status)
DELETE /api/v1/upload-requests/{id}         revoke (sets status=revoked, revoked_at)
GET    /api/v1/upload-requests/{id}/submissions   list submissions (files uploaded count, date)
```

### Create body

```json
{
  "collection": "inbox",
  "target_folder": "q2-deliverables",
  "label": "Q2 Deliverables Upload",
  "description": "Please upload your Q2 report here. PDF or DOCX accepted.",
  "file_metadata": {
    "access": { "visibility": "private" },
    "crawl":  { "crawlable": false, "indexable": false }
  },
  "allow_uploader_access": false,
  "single_use": false,
  "use_submission_folders": true,
  "max_files": 50,
  "max_file_size": 52428800,
  "expires_at": "2026-08-01T00:00:00Z"
}
```

### Create response

```json
{
  "request_id": "uRq7kZ9pLmXw3fNa",
  "url": "https://artifacts.example.com/r/uRq7kZ9pLmXw3fNa",
  "collection": "inbox",
  "target_folder": "q2-deliverables",
  "status": "active",
  "files_uploaded": 0,
  "created_at": "2026-06-28T10:00:00Z",
  "expires_at": "2026-08-01T00:00:00Z"
}
```

### PATCH body (partial, fields to change)

```json
{ "label": "Updated label", "expires_at": "2026-09-01T00:00:00Z" }
```

Close without revoking (no new uploads, existing files kept):
```json
{ "status": "closed" }
```

## 6. CLI commands

```
artifactctl upload-request create <collection>
        [--target-folder <prefix>]
        [--label <text>] [--description <text>]
        [--expires-in <dur>] [--expires <ts>]
        [--max-files <n>] [--max-file-size <size>]     # human sizes, 1024-based
        [--public|--unlisted|--protected|--private]    # file_metadata visibility
        [--crawlable|--no-crawlable] [--index|--no-index]
        [--allow-uploader-access]
        [--single-use]
        [--use-submission-folders]

artifactctl upload-request list
artifactctl upload-request show <request-id>      # `get` kept as an alias
artifactctl upload-request close <request-id>     # stops new uploads; files kept
artifactctl upload-request revoke <request-id>    # permanent; escalates a closed link
artifactctl upload-request submissions <request-id>
```

`create` prints the upload URL prominently so it can be copied and shared.
`--output json` returns machine-readable JSON with the full record.

### CLI → API mapping

| CLI | API |
|-----|-----|
| `upload-request create` | `POST /api/v1/upload-requests` |
| `upload-request list` | `GET /api/v1/upload-requests` |
| `upload-request show` | `GET /api/v1/upload-requests/{id}` |
| `upload-request close` | `PATCH /api/v1/upload-requests/{id}` (`status=closed`) |
| `upload-request revoke` | `DELETE /api/v1/upload-requests/{id}` |
| `upload-request submissions` | `GET /api/v1/upload-requests/{id}/submissions` |

## 7. Admin UI

### New screen: Upload Requests (under the existing Shares nav item, or a new top-nav entry)

| Section | Contents |
|---------|----------|
| Upload Requests list | Table: label, collection/folder, status, files uploaded, expires, actions |
| Per-request actions | Copy link, View submissions, Close, Revoke |
| Create form | All fields from API create body; shows resulting URL on save |
| Submissions view | List of submission rows with file count, date, client IP |

The create form shows the link URL prominently with a copy button after creation
(same UX as tokens — shown once, easy to copy). The URL is also shown in the
list and detail views since upload request links are designed to be shared.

Add an **"Upload Requests"** entry to the top-nav bar (alongside Collections,
Shares, Tokens, Audit).

Keyboard shortcut: `Alt-U` → Upload Requests (consistent with the retro NC
footer pattern from [09 §1](09-admin-ui.md)).

## 8. MCP tools

| Tool | Maps to | Notes |
|------|---------|-------|
| `artifact_create_upload_request` | `POST /api/v1/upload-requests` | returns URL; agent can share it |
| `artifact_get_upload_request` | `GET /api/v1/upload-requests/{id}` | status, files uploaded |
| `artifact_list_upload_requests` | `GET /api/v1/upload-requests` | paginated list |
| `artifact_revoke_upload_request` | `DELETE /api/v1/upload-requests/{id}` | close the link |

## 9. Security

- `request_id` is ≥ 95 bits from `crypto/rand` (same as share IDs, decision D2
  in [14 §4](14-access-control.md)). Possession is the capability.
- `POST /r/{request_id}` needs **no additional auth** — same pattern as upload
  sessions ([14 §5](14-access-control.md)).
- Size enforcement: `max_file_size` per request ≤ server `max_upload_size`; 413
  if exceeded. The server's global `MaxBytesReader` is the hard limit.
- Quota enforcement: `max_files` counter is incremented atomically inside a DB
  transaction; 409 if exceeded.
- Single-use enforcement: when `single_use = true`, `status` is set to `closed`
  inside the same DB transaction that commits the uploaded files. A concurrent
  POST racing on the same link loses the transaction and receives 410.
- Expired/revoked links return **410 Gone** — informative for holders of a
  legitimate link, while the unguessable ID prevents enumeration.
- Rate limiting: same per-IP rate limiter applied to auth attempts ([06](06-auth-security.md))
  covers repeated POSTs from a single IP; consider a separate per-link rate
  limit to prevent a single IP flooding a request.
- Audit log: each upload via a request link is recorded (`upload_request.upload`,
  actor = anonymous/client-ip, target = collection/path).
- Files uploaded via a request link carry the configured `file_metadata`
  (visibility, crawl, expiry) — the admin fully controls what happens to them.

## 10. Expiry & janitor integration

- Upload requests with `expires_at` in the past are set `status = expired` by
  the janitor sweep (same pass as nodes/shares).
- Expired/revoked requests return 410 on GET and POST.
- Revoking a request does **not** delete already-uploaded files — they remain
  in the collection under their regular policy. The admin deletes them separately
  if desired.
- `DELETE /api/v1/upload-requests/{id}` revokes (no file deletion). A future
  `?delete_files=true` query parameter can cascade-delete uploaded files — not
  in scope for this epic.

## 11. Scope

| Item | Included in this epic |
|------|-----------------------|
| Data model + migration | Yes |
| API endpoints (CRUD + submissions) | Yes |
| Public upload form (`GET /r/{id}`) | Yes |
| File upload handler (`POST /r/{id}`) | Yes |
| CLI (`upload-request` subcommand) | Yes |
| Admin UI (Upload Requests screen) | Yes |
| MCP tools (4 tools) | Yes |
| Per-link rate limiting | Deferred (file a follow-up task if needed) |
| `?delete_files=true` cascade on revoke | Deferred |
| Resumable uploads via request link | Deferred (add Content-Range support later) |
