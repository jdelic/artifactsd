# 03 — Metadata & policy resolution

## 1. The metadata document

Every collection, folder, file, and share may carry a metadata document with the
same shape (SPEC §6). All sections are optional; absent fields inherit.

```yaml
content:
  content_type: text/html; charset=utf-8
  headers:
    cache-control: no-store
    access-control-allow-origin: "*"

access:
  visibility: public | unlisted | protected | private
  directory_listing: true | false
  auth:                       # any-of; empty means no auth required
    - type: bearer
      token_hash: <sha256-hex>
    - type: basic
      username: demo
      password_hash: <argon2id>
    - type: oidc
      issuer: https://accounts.example.com
      audience: artifactd
      allowed_subjects: [user@example.com]

crawl:
  crawlable: true | false
  indexable: true | false
  robots:
    user_agents:
      "*": { allow: [/], disallow: [/private/] }
    x_robots_tag: [noindex]

expiry:
  expires_at: 2026-07-01T00:00:00Z
  delete_after_last_access: 30d
  delete_after_creation: 7d
  inherit: true | false       # see §4
```

### Go representation
`meta.Document` is a struct with pointer/optional fields so "unset" is
distinguishable from "set to zero". Stored as JSON in the `metadata` columns.
On the wire (API) it is JSON; in config/CLI it may be YAML and is converted.
**Secrets are never stored or returned in cleartext**: the API accepts
`token`/`password` on write, hashes them, and persists only `token_hash` /
`password_hash`. Reads return the hash presence, never the secret.

## 2. Resolution order (inheritance)

Effective metadata for a served object is computed by merging, in increasing
precedence:

```
1. server config `defaults`        (SPEC §19)
2. collection metadata
3. folder metadata, outermost → innermost along the path
4. file metadata
5. share metadata                  (only when accessed via /s/{share_id})
```

Later layers override earlier ones. Merge semantics per section:

| Section | Merge rule |
|---------|-----------|
| `content.content_type` | scalar override (last wins) |
| `content.headers` | **map merge** by header name; child overrides same key; empty string value removes an inherited header |
| `access.visibility` | scalar override |
| `access.directory_listing` | scalar override |
| `access.auth` | **replace** (a level that sets `auth` replaces the inherited list; not appended — avoids accidental privilege widening). Empty list `[]` explicitly means "no auth". |
| `crawl.*` | scalar/map override like `access` |
| `expiry` | special, see §4 |

Rationale for `auth` = replace: appending would let a child silently add an
extra accepted credential to a protected parent; replacing makes each level's
auth intent explicit and auditable.

## 3. Visibility semantics (SPEC §8.1)

| Mode | Public fetch (`/c`, `/s`) | Appears in listings/sitemaps | Notes |
|------|---------------------------|------------------------------|-------|
| `public` | yes | yes (if `crawl.indexable` for sitemap; always eligible for dir listing) | |
| `unlisted` | yes (if URL known) | no | excluded from generated indexes, robots `Disallow`-friendly |
| `protected` | only with valid auth | no | 401/403 without auth |
| `private` | never via public routes | no | management API only; public routes return 404 |

Effective visibility is the **most restrictive** encountered? No — visibility is
an override (last-wins), but a child cannot be *more* public than a `private`
ancestor when that would leak content: see precedence guard in §5.

## 4. Expiry resolution (SPEC §11)

Three inputs may each appear at any level:
- `expires_at` — absolute instant.
- `delete_after_creation: <dur>` — relative to the object's `created_at`.
- `delete_after_last_access: <dur>` — relative to `last_access_at` (or creation
  if never accessed).

Effective absolute expiry for an object =
`min(` all applicable resolved instants from the object **and inherited
levels** `)` — "the earliest effective expiry wins" (SPEC §11).

Override: a level may set `expiry.inherit: false` to **stop** inheriting
ancestor expiry (only its own expiry rules apply). This is the "unless
explicitly configured otherwise" escape hatch in SPEC §11. Default
`inherit: true`.

Durations parse Go-style plus `d`/`w` (e.g. `30d`, `48h`, `2w`). The computed
absolute `expires_at` is written to the denormalized column so the janitor can
query it directly; `delete_after_last_access` makes it a moving target, so it is
recomputed when `last_access_at` advances (throttled — see [08](08-expiry-janitor.md)).

## 5. Policy evaluation

`policy.Decide(effective, request) → Decision` returns one of:
`Serve`, `Unauthorized(401)`, `Forbidden(403)`, `NotFound(404)`.

Algorithm for public serve routes:
1. If object/effective is `private` → **404** (never reveal existence, SPEC §17).
2. If expired → **404** (SPEC §11, not 403).
3. If `protected`:
   - No credentials presented → 401 with appropriate `WWW-Authenticate`.
   - Credentials presented but none in `access.auth` match → 403.
   - A match → continue.
4. If `public`/`unlisted` → continue (no auth).
5. Directory request (`.../` path):
   - allowed only if `access.directory_listing` is true **and** visibility
     permits; otherwise 404 (listing off) — but a known file under it still
     serves.

**Ancestor guard:** when resolving, if any ancestor is `private`, the object is
treated as private regardless of a more-public child override on public routes
(prevents a public file under a private collection from leaking). A child may be
*more* restrictive than its parent freely. This guard is applied after merge,
before the decision.

## 6. Header safety

`content.headers` is filtered before emission. Rejected (hop-by-hop / unsafe,
SPEC §7.2): `connection`, `transfer-encoding`, `content-length`, `host`,
`keep-alive`, `upgrade`, `te`, `trailer`, `proxy-authenticate`,
`proxy-authorization`. `content-type` from headers is ignored in favor of the
resolved `content.content_type`. Setting these via API/CLI is rejected at write
time with a 400, and re-filtered at serve time as defense in depth.

## 7. Crawler/indexing headers at serve time

- `crawl.indexable: false` → emit `X-Robots-Tag: noindex` (plus any configured
  `crawl.robots.x_robots_tag` values).
- These are independent of access (SPEC §10): a `public` object can still be
  non-indexable.

See [05](05-serving-crawl.md) for robots.txt and sitemap generation, which read
the same resolved `crawl`/`access` data.
