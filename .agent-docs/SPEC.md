# artifactd: Self-Contained Artifact Publishing Server

> **Status: original specification (requirements anchor).**
> This is the source-of-truth for *intent* — the object/URL model, use cases
> (§20), and security requirements. The detailed **design** lives in
> [`design/`](design/README.md) and the plan in [`ROADMAP.md`](ROADMAP.md);
> where they differ, **they are authoritative**. Section numbers here are kept
> stable because the design docs cite them as `SPEC §X`.
>
> **Amendments adopted during planning** (see
> [`design/README.md`](design/README.md) decision table and the answered QA):
> - **Name:** the product/daemon is **`artifactsd`** (canonical), not
>   `artifactd`. Env prefix `ARTIFACTSD_`, db `artifactsd.db`, data dir
>   `artifactsd-data`, header `Artifactsd-Metadata`. CLI stays `artifactctl`.
> - **OIDC and ACME/automatic TLS are in the MVP** (not deferred); OIDC also
>   gates the admin UI. See [`design/14-access-control.md`](design/14-access-control.md).
> - **Default visibility is `public`** (no auth unless configured; listing/
>   crawl/index off by default), overriding the `private` default in §19.
> - **Configuration is flags + environment only** (12factor); there is **no
>   config file**. The YAML in §19 illustrates the logical settings only — see
>   [`design/12-config.md`](design/12-config.md).
> - **Logging:** access logs → stdout, app/errors → stderr, audit → DB + stderr.
>   There is no `access_logs` table or admin access-log view.
> - **Expiry:** earliest-in-tree always wins (no inheritance override; a
>   container's deletion cascades to its children).
> - **Upload URLs are unguessable capabilities** needing no extra auth.
> - **License:** BSD 3-Clause © Jonas Maurus.

## 1. Goal
`artifactd` is a self-contained Go server for storing, managing, and publishing artifacts created by AI agents, automation systems, CI jobs, and humans.
Artifacts may be HTML pages, PDFs, images, audio files, videos, JSON files, logs, archives, or any other file type. Uploaded artifacts are made available over HTTP with correct content types, configurable access control, optional directory browsing, expiry-based cleanup, and crawler/indexing policy.
The service is designed to be easy to run as a single binary:
```javascript
artifactd serve --listen :443 --data ./artifactd-data --base-url https://artifacts.example.com
```
The system provides:
- Public and protected HTTP file serving
- Per-file, per-folder, and per-collection metadata
- Bearer token, Basic Auth, and OIDC-based access protection
- Automatic expiry and unattended deletion of old files
- Human-friendly browsable URLs
- Hard-to-guess share URLs
- Browsable and unbrowsable file trees
- Authenticated crawl/list APIs for agents
- `robots.txt` and indexing policy generation
- Admin web interface
- CLI interface
- Optional MCP adapter for AI agents
`artifactd` is HTTP-first. MCP is an adapter, not the core storage protocol.


## 2. Non-goals
The initial version does not require:
- Nginx, Caddy, Apache, or any other external web server
- External databases
- S3 or cloud object storage
- Multi-node clustering
- CDN integration
- Accelerated internal redirect serving
- Complex workflow orchestration
The first implementation should be a single Go executable using local filesystem storage and SQLite metadata.


## 3. Architecture
```javascript
artifactd
├── Public HTTP file server
├── HTTP management API
├── Admin web interface
├── CLI client
├── MCP adapter
├── SQLite metadata database
├── Local blob storage
└── Expiry janitor
```
The HTTP API is the canonical interface.
The CLI uses the HTTP API.
The MCP server exposes selected agent-friendly tools that call the same HTTP API internally.


## 4. Object model
The storage hierarchy is:
```javascript
collection/
  folder/
    file
```
A collection is the top-level namespace for a published artifact tree.
Examples:
```javascript
sales-demo-2026/
  index.html
  assets/chart.png
  report.pdf

run-8f31c2/
  logs/output.txt
  audio/summary.mp3
  result.json
```
Each object may have metadata:
- Collection metadata
- Folder metadata
- File metadata
Metadata inherits downward unless overridden.
For example, a collection may require bearer-token access, while a specific file may be public.


## 5. URL model
### 5.1 Human-readable collection URLs
Files are served at:
```javascript
https://{base_url}/c/{collection}/{path}
```
Example:
```javascript
https://artifacts.example.com/c/sales-demo-2026/index.html
https://artifacts.example.com/c/sales-demo-2026/report.pdf
```
### 5.2 Hard-to-guess share URLs
Collections, folders, or files may also be exposed through unguessable share URLs:
```javascript
https://{base_url}/s/{share_id}/{optional_path}
```
Example:
```javascript
https://artifacts.example.com/s/7V9qWb3KxLm2/report.pdf
```
Share URLs may have their own access policy, expiry time, and listing behavior.
### 5.3 API URLs
Management operations use:
```javascript
https://{base_url}/api/v1/...
```
Example:
```javascript
PUT /api/v1/collections/sales-demo-2026/files/report.pdf
```


## 6. Metadata model
Metadata should be explicit and structured.
Example:
```javascript
content:
  content_type: text/html; charset=utf-8
  headers:
    cache-control: no-store
    access-control-allow-origin: "*"

access:
  visibility: public
  directory_listing: true
  auth: []

crawl:
  crawlable: true
  indexable: false
  robots:
    user_agents:
      "*":
        allow:
          - /
        disallow:
          - /private/
    x_robots_tag:
      - noindex

expiry:
  expires_at: 2026-07-01T00:00:00Z
  delete_after_last_access: 30d
```


## 7. Content metadata
### 7.1 `content.content_type`
Sets the HTTP `Content-Type` header.
Example:
```javascript
content:
  content_type: application/pdf
```
If omitted, the server may detect the content type from the filename or file contents.
### 7.2 `content.headers`
Additional HTTP response headers to send when the file is served.
Example:
```javascript
content:
  headers:
    cache-control: public, max-age=3600
    access-control-allow-origin: "*"
```
The server should reject unsafe hop-by-hop headers such as:
```javascript
connection
transfer-encoding
content-length
host
```


## 8. Access policy
Access policy controls who may fetch or browse content.
```javascript
access:
  visibility: public | unlisted | protected | private
  directory_listing: true | false
  auth:
    - type: bearer
      token_hash: ...
    - type: basic
      username: demo
      password_hash: ...
    - type: oidc
      issuer: https://accounts.example.com
      audience: artifactd
      allowed_subjects:
        - user@example.com
```
### 8.1 Visibility modes
#### `public`
The object can be fetched without authentication.
#### `unlisted`
The object can be fetched without authentication if the caller knows the URL, but it should not appear in public listings or generated public indexes.
#### `protected`
The object requires one of the configured authentication mechanisms.
#### `private`
The object is hidden from public serving and is only accessible through authenticated management APIs.


## 9. Directory listing
Directory listing is controlled separately from file access.
```javascript
access:
  directory_listing: true
```
If enabled, browsers may see a generated listing page for a collection or folder.
Example:
```javascript
GET /c/sales-demo-2026/
```
May return an HTML listing of:
```javascript
index.html
report.pdf
assets/
```
If disabled, fetching a known file may still work, but browsing the folder does not.
This allows:
- Public browsable collections
- Public but unlisted files
- Protected browsable workspaces
- Agent-crawlable but human-unbrowsable trees


## 10. Crawl and indexing policy
Crawl policy controls whether automated clients may enumerate, crawl, or index content.
This is separate from access control.
```javascript
crawl:
  crawlable: true
  indexable: false
```
### 10.1 `crawl.crawlable`
If true, authenticated agents with suitable permissions may enumerate the object through crawl APIs.
If false, the object should not appear in crawl APIs unless the caller has admin permissions.
### 10.2 `crawl.indexable`
If true, the server may allow public indexing by search engines and other crawlers.
If false, the server should discourage indexing by emitting suitable headers and robots policy.
Example for non-indexable content:
```javascript
X-Robots-Tag: noindex
```
### 10.3 Public crawler policy
`artifactd` exposes a root robots endpoint:
```javascript
GET /robots.txt
```
The generated `robots.txt` should reflect global and collection-level crawl/index policy.
Example:
```javascript
User-agent: *
Disallow: /api/
Disallow: /admin/
Disallow: /c/private-run/
Allow: /c/public-demo/
```
Collections may also expose collection-specific crawler information:
```javascript
GET /c/{collection}/robots.txt
```
Example:
```javascript
User-agent: *
Allow: /
Disallow: /private/
Sitemap: https://artifacts.example.com/c/sales-demo-2026/sitemap.xml
```
### 10.4 Sitemap generation
For public and indexable collections, the server may expose:
```javascript
GET /sitemap.xml
GET /c/{collection}/sitemap.xml
```
Only public, indexable URLs should appear in sitemaps.
Protected, private, expired, unlisted, or non-indexable objects must not appear.
### 10.5 Agent crawl API
Agents should not scrape HTML listings. They should use the authenticated crawl API:
```javascript
GET /api/v1/crawl
GET /api/v1/collections/{collection}/crawl
```
Example response:
```javascript
{
  "collection": "sales-demo-2026",
  "base_url": "https://artifacts.example.com/c/sales-demo-2026/",
  "crawlable": true,
  "indexable": false,
  "entries": [
    {
      "path": "index.html",
      "type": "file",
      "url": "https://artifacts.example.com/c/sales-demo-2026/index.html",
      "content_type": "text/html; charset=utf-8",
      "size": 18422,
      "updated_at": "2026-06-21T10:15:00Z",
      "expires_at": "2026-07-01T00:00:00Z",
      "crawlable": true,
      "indexable": false
    },
    {
      "path": "assets/",
      "type": "folder",
      "crawlable": true,
      "indexable": false
    }
  ]
}
```
This gives agents a reliable way to understand what exists without relying on browser-oriented directory pages.


## 11. Expiry and deletion
Objects may expire automatically.
```javascript
expiry:
  expires_at: 2026-07-01T00:00:00Z
```
The expiry janitor periodically deletes expired files, folders, collections, shares, and metadata.
Supported expiry modes:
```javascript
expiry:
  expires_at: 2026-07-01T00:00:00Z
  delete_after_last_access: 30d
  delete_after_creation: 7d
```
Rules:
- Expiry may be set on collections, folders, files, and shares.
- Child objects inherit expiry unless overridden.
- The earliest effective expiry wins unless explicitly configured otherwise.
- Expired objects should return `404 Not Found`, not `403 Forbidden`.
- Deletion should remove blob data and metadata.
- Admin logs should record expiry-based deletion.


## 12. HTTP management API
### 12.1 Collections
Create collection:
```javascript
POST /api/v1/collections
Content-Type: application/json

{
  "name": "sales-demo-2026",
  "metadata": {
    "access": {
      "visibility": "public",
      "directory_listing": true
    },
    "crawl": {
      "crawlable": true,
      "indexable": false
    }
  }
}
```
List collections:
```javascript
GET /api/v1/collections
```
Delete collection:
```javascript
DELETE /api/v1/collections/sales-demo-2026
```
### 12.2 Files
Upload small file directly:
```javascript
PUT /api/v1/collections/sales-demo-2026/files/index.html
Content-Type: text/html; charset=utf-8

<!doctype html>
<html>
  <body>Hello from artifactd</body>
</html>
```
Upload with metadata:
```javascript
PUT /api/v1/collections/sales-demo-2026/files/report.pdf
Content-Type: application/pdf
Artifactd-Metadata: eyJleHBpcnkiOns...base64-json...

...binary pdf data...
```
Read metadata:
```javascript
GET /api/v1/collections/sales-demo-2026/files/report.pdf/meta
```
Patch metadata:
```javascript
PATCH /api/v1/collections/sales-demo-2026/files/report.pdf/meta
Content-Type: application/json

{
  "content": {
    "content_type": "application/pdf"
  },
  "access": {
    "visibility": "protected",
    "auth": [
      {
        "type": "bearer",
        "token": "demo-token"
      }
    ]
  },
  "expiry": {
    "expires_at": "2026-07-01T00:00:00Z"
  }
}
```
Delete file:
```javascript
DELETE /api/v1/collections/sales-demo-2026/files/report.pdf
```
### 12.3 Large uploads
For larger files, clients request an upload URL.
```javascript
POST /api/v1/collections/sales-demo-2026/uploads
Content-Type: application/json

{
  "path": "audio/summary.mp3",
  "metadata": {
    "content": {
      "content_type": "audio/mpeg"
    },
    "expiry": {
      "delete_after_creation": "14d"
    }
  }
}
```
Response:
```javascript
{
  "upload_url": "https://artifacts.example.com/api/v1/uploads/upl_8f31c2",
  "method": "PUT",
  "expires_at": "2026-06-21T11:00:00Z"
}
```
Client uploads bytes directly:
```javascript
PUT /api/v1/uploads/upl_8f31c2
Content-Type: audio/mpeg

...mp3 bytes...
```


## 13. Public serving behavior
When serving files, `artifactd` should:
- Resolve effective metadata from collection, folder, file, and share
- Check expiry
- Check access policy
- Set `Content-Type`
- Set configured safe headers
- Set crawler/indexing headers
- Serve the file body
Example:
```javascript
GET /c/sales-demo-2026/report.pdf
```
Response:
```javascript
HTTP/1.1 200 OK
Content-Type: application/pdf
Cache-Control: no-store
X-Robots-Tag: noindex

...pdf bytes...
```
Protected file example:
```javascript
GET /c/sales-demo-2026/report.pdf
Authorization: Bearer demo-token
```
Basic Auth example:
```javascript
GET /c/demo/index.html
Authorization: Basic ZGVtbzpwYXNzd29yZA==
```


## 14. Admin web interface
The admin web interface should allow operators to:
- View all collections
- Browse file trees
- Upload and delete files
- Edit metadata
- Create and revoke share URLs
- Configure access policy
- Configure crawl and indexing policy
- View expiry settings
- View deletion logs
- View access logs
- Manage API tokens
- Manage OIDC settings
- Trigger cleanup jobs manually
The admin interface is served by the same binary:
```javascript
https://artifacts.example.com/admin
```
Admin access must always require authentication.


## 15. CLI
The CLI should be easy for agents, humans, and scripts.
Examples:
Create a collection:
```javascript
artifactctl collection create sales-demo-2026 \
  --public \
  --list \
  --crawlable \
  --no-index
```
Upload a file:
```javascript
artifactctl put sales-demo-2026/report.pdf ./report.pdf \
  --content-type application/pdf \
  --expires 2026-07-01T00:00:00Z
```
Upload a folder:
```javascript
artifactctl put-tree sales-demo-2026 ./dist \
  --content-type-auto \
  --public \
  --no-index
```
Create a protected share:
```javascript
artifactctl share create sales-demo-2026/report.pdf \
  --bearer-token \
  --expires-in 7d
```
List a tree:
```javascript
artifactctl tree sales-demo-2026
```
Fetch crawl metadata:
```javascript
artifactctl crawl sales-demo-2026
```


## 16. MCP adapter
The MCP adapter exposes agent-friendly tools over the HTTP API.
MCP is not the canonical storage layer.
Recommended MCP tools:
```javascript
artifact_create_collection
artifact_put_text
artifact_begin_upload
artifact_get_metadata
artifact_set_metadata
artifact_delete
artifact_list_tree
artifact_get_public_url
artifact_create_share
artifact_get_crawl_tree
```
Small text artifacts may be uploaded directly through MCP.
Large or binary artifacts should use `artifact_begin_upload`, which returns a direct HTTP upload URL.
Example MCP flow:
```javascript
1. artifact_create_collection("run-8f31c2")
2. artifact_put_text("run-8f31c2", "index.html", "<html>...</html>")
3. artifact_begin_upload("run-8f31c2", "report.pdf", "application/pdf")
4. Agent PUTs PDF bytes to returned upload URL
5. artifact_create_share("run-8f31c2", visibility="unlisted", expires_in="7d")
6. Agent returns share URL to user
```


## 17. Security requirements
The server should:
- Store bearer tokens hashed, not in plaintext
- Store Basic Auth passwords hashed, not in plaintext
- Support TLS directly
- Allow operation behind a reverse proxy, but not require one
- Validate paths and prevent traversal attacks
- Reject unsafe metadata headers
- Enforce maximum upload sizes
- Rate-limit authentication failures
- Log admin actions
- Log deletion actions
- Avoid leaking existence of protected/private files
- Return `404 Not Found` for private or expired objects where appropriate


## 18. Storage
Initial implementation:
```javascript
data/
  artifactd.db
  blobs/
    collections/
      sales-demo-2026/
        index.html
        report.pdf
```
SQLite stores:
- Collections
- File metadata
- Folder metadata
- Share records
- Access policies
- Expiry rules
- Upload sessions
- Audit logs
The filesystem stores blob bytes.


## 19. Configuration
Example config file:
```javascript
server:
  listen: ":443"
  base_url: "https://artifacts.example.com"
  tls:
    cert_file: "./cert.pem"
    key_file: "./key.pem"

storage:
  data_dir: "./artifactd-data"

admin:
  auth:
    type: basic
    username: admin
    password_hash: "$argon2id$..."

defaults:
  access:
    visibility: private
    directory_listing: false
  crawl:
    crawlable: false
    indexable: false
  expiry:
    delete_after_creation: 30d

limits:
  max_upload_size: 1073741824
  max_file_count_per_collection: 10000
```


## 20. Example use cases
### 20.1 Public HTML report, not indexable
```javascript
artifactctl collection create quarterly-report \
  --public \
  --list \
  --crawlable \
  --no-index

artifactctl put-tree quarterly-report ./dist \
  --content-type-auto
```
Result:
```javascript
https://artifacts.example.com/c/quarterly-report/index.html
```
Search engines are discouraged from indexing it, but authorized agents may crawl the tree.


### 20.2 Private agent run with expiring share URL
```javascript
artifactctl collection create run-8f31c2 \
  --private \
  --expires-in 14d

artifactctl put-tree run-8f31c2 ./outputs

artifactctl share create run-8f31c2 \
  --unlisted \
  --expires-in 48h
```
Result:
```javascript
https://artifacts.example.com/s/7V9qWb3KxLm2/
```
The share URL is hard to guess and expires automatically.


### 20.3 Protected PDF with bearer token
```javascript
artifactctl put client-demo/report.pdf ./report.pdf \
  --content-type application/pdf \
  --protected \
  --bearer-token demo-token
```
Fetch:
```javascript
curl \
  -H "Authorization: Bearer demo-token" \
  https://artifacts.example.com/c/client-demo/report.pdf
```


### 20.4 Public assets with CORS
```javascript
artifactctl put-tree web-assets ./public \
  --public \
  --header 'access-control-allow-origin=*' \
  --header 'cache-control=public, max-age=3600'
```


## 21. MVP scope
The first useful version should include:
- Single Go binary
- Local filesystem blob storage
- SQLite metadata
- HTTP file serving
- HTTP management API
- Basic Auth admin UI
- Bearer-token protected files
- Content-Type metadata
- Expiry janitor
- Directory listing on/off
- Public, unlisted, protected, private visibility
- `robots.txt`
- `X-Robots-Tag`
- Agent crawl API
- CLI
- Minimal MCP adapter
OIDC can come after the basic access model is stable.


## 22. Naming
Working name:
```javascript
artifactd
```
CLI:
```javascript
artifactctl
```
Core description:
```javascript
A self-hosted artifact publishing server for agents and humans, with per-object access policy, expiry, browsing, and crawl controls.
```
