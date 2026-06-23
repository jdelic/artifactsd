# 05 — Public serving, crawl, robots & sitemap

## 1. Public serving pipeline (SPEC §13)

Routes: `GET|HEAD /c/{collection}/{path...}` and
`GET|HEAD /s/{share_id}/{path...}`.

Steps:
1. **Resolve target.** For `/c`: `(collection, path)`. For `/s`: look up
   `share_id` → `(collection, base_path)`, then append the optional tail path,
   re-validating it stays within the share's subtree.
2. **Existence/expiry.** Load node; if absent or effective-expired → **404**.
3. **Resolve effective metadata** ([03](03-metadata-policy.md)); for `/s`,
   include the share layer (highest precedence).
4. **Policy decision** ([03 §5](03-metadata-policy.md)): 401 / 403 / 404 / serve.
5. **Directory vs file.**
   - Trailing slash or folder target → directory request.
     - If `directory_listing` true & permitted → render listing (HTML for
       browsers via `Accept`, or `?format=json`).
     - Else, try an index document (`index.html`) if present; otherwise 404.
   - File target → serve the blob.
6. **Emit headers** then body:
   - `Content-Type` = resolved `content.content_type` (else detected).
   - Safe `content.headers` (filtered, [03 §6](03-metadata-policy.md)).
   - `X-Robots-Tag: noindex` when not indexable (+ configured tags).
   - `ETag` (from checksum), `Last-Modified`, `Accept-Ranges: bytes`,
     conditional/range support via `http.ServeContent`.
7. **Side effects:** access-log entry to stdout; throttled `last_access_at`
   update (drives `delete_after_last_access`); **if the object was
   auth-protected and the caller authenticated successfully, write an
   `object.access` row to the audit log** (actor = authenticated principal),
   decision N6 / [06 §7](06-auth-security.md). Public reads are not audited.

`HEAD` runs the same pipeline without the body. Range requests and conditional
GETs are handled by `http.ServeContent` over an `io.ReadSeeker` from the blob
store.

### Content-type detection
Order: explicit `content.content_type` → stored `content_type` → extension map
(`mime.TypeByExtension` + a curated table for common artifact types) →
`http.DetectContentType` sniff of the first 512 bytes. HTML/SVG/text default to
`; charset=utf-8`.

## 2. Directory listing

Generated listing for a folder shows immediate children (files + subfolders),
each with name, size, content-type, modified time. Honors visibility: `private`
children are omitted; `unlisted` children are omitted from listings (SPEC §8.1)
even though directly fetchable. HTML listing is a styled `html/template`; JSON
listing mirrors the crawl entry shape for programmatic use.

## 3. Robots policy (SPEC §10.3)

```
GET /robots.txt
GET /c/{collection}/robots.txt
```

Root `robots.txt` is generated from global config + per-collection crawl policy:
- Always `Disallow: /api/` and `Disallow: /admin/`.
- For each collection: `Allow: /c/{name}/` if `crawl.indexable`, else
  `Disallow: /c/{name}/`. `private`/`unlisted` collections are disallowed.
- Emits `Sitemap: {base_url}/sitemap.xml` when any indexable content exists.

Collection `robots.txt` reflects that collection's `crawl.robots` block
(user-agent allow/disallow lists) and a `Sitemap:` line to its own sitemap when
indexable (SPEC §10.3 example).

Results are cached in-memory with a short TTL and invalidated on metadata
changes.

## 4. Sitemap generation (SPEC §10.4)

```
GET /sitemap.xml
GET /c/{collection}/sitemap.xml
```

Includes **only** objects that are simultaneously `public` **and**
`crawl.indexable` and not expired/unlisted/private (SPEC §10.4). Backed by the
denormalized `visibility`/`expires_at` columns plus an `indexable` filter. Large
sitemaps use a sitemap index splitting at 50k URLs. `<lastmod>` from
`updated_at`.

## 5. Agent crawl API (SPEC §10.5)

Authenticated, structured, policy-filtered enumeration so agents never scrape
HTML (SPEC §10.5):

```
GET /api/v1/crawl
GET /api/v1/collections/{collection}/crawl?path=&depth=
```

Response (SPEC §10.5):
```json
{ "collection":"sales-demo-2026",
  "base_url":"https://artifacts.example.com/c/sales-demo-2026/",
  "crawlable":true, "indexable":false,
  "entries":[
    {"path":"index.html","type":"file",
     "url":"https://.../c/sales-demo-2026/index.html",
     "content_type":"text/html; charset=utf-8","size":18422,
     "updated_at":"2026-06-21T10:15:00Z","expires_at":"2026-07-01T00:00:00Z",
     "crawlable":true,"indexable":false},
    {"path":"assets/","type":"folder","crawlable":true,"indexable":false}
  ] }
```

Rules:
- Objects with `crawl.crawlable:false` are omitted unless the token has `admin`
  scope (SPEC §10.1).
- Expired and `private` objects are never listed.
- `depth` controls recursion; default 1 (immediate children), `depth=-1` full
  tree.
- Distinct from `/api/v1/collections/{c}/tree` (admin, unfiltered) in
  [04 §4](04-http-api.md).

## 6. Caching & performance notes

- robots/sitemap responses cached with TTL + invalidation hook on writes.
- Listing/crawl queries use the `parent_id` index; one query per directory level.
- Blob serving streams from disk with `ServeContent`; no full-file buffering.
