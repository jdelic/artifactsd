# 03 — Metadata & policy resolution

## 1. The metadata document

Every collection, folder, file, and share may carry a metadata document with the
same shape (SPEC §6). All sections are optional; absent fields inherit.

```yaml
content:
  content_type: text/html; charset=utf-8
  sandbox: true | false       # default true: serve active content in a CSP sandbox
  headers:
    cache-control: no-store
    access-control-allow-origin: "*"

access:
  visibility: public | unlisted | protected | private
  directory_listing: true | false
  auth:                       # any-of; empty/absent means no auth required (default)
    - type: bearer
      token_hash: <sha256-hex>
    - type: basic             # "htaccess"-style; triggers browser login dialog
      username: demo
      password_hash: <argon2id>
    - type: oidc
      issuer: https://accounts.example.com
      audience: artifactsd
      # any of the following grant access (any-of within the entry):
      allowed_subjects: [user@example.com]   # whitelist by sub / email
      allowed_groups: [staff]                # whitelist by group claim
      group_claim: groups                    # claim to read groups from (default: groups)
      any_authenticated: false               # true => any valid token from issuer

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
```

Access control is the heart of this system; the full model (the three auth
mechanisms, OIDC group/subject/any-authenticated whitelisting, and admin
gating) is consolidated in [14 — Access control](14-access-control.md).

### Go representation
`meta.Document` is a struct with pointer/optional fields so "unset" is
distinguishable from "set to zero". Stored as JSON in the `metadata` columns.
On the wire (API) it is JSON; in the CLI it may be YAML and is converted. (This
is per-object metadata, distinct from server configuration, which is
flags+env only — see [12](12-config.md).)
**Secrets are never stored or returned in cleartext**: the API accepts
`token`/`password` on write, hashes them, and persists only `token_hash` /
`password_hash`. Reads return the hash presence, never the secret.

## 2. Resolution order (inheritance)

Effective metadata for a served object is computed by merging, in increasing
precedence:

```
1. server defaults                 (from flags/env, see 12)
2. collection metadata
3. folder metadata, outermost → innermost along the path
4. file metadata
5. share metadata                  (only when accessed via /s/{share_id})
```

Later layers override earlier ones. Merge semantics per section:

| Section | Merge rule |
|---------|-----------|
| `content.content_type` | scalar override (last wins) |
| `content.sandbox` | scalar override (last wins) |
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
| `protected` | only with valid auth | no | challenge depends on auth type, see §5 |
| `private` | never via public routes | no | management API only; public routes return 404 |

**Default visibility is `public`** (operator decision C5): objects do not require
auth unless `access.auth` is configured, and are reachable at their URL (which
may be deliberately hard to guess). `directory_listing`, `crawl.crawlable`, and
`crawl.indexable` still default to **false**, so a default object is fetchable by
URL but not listed, crawled, or indexed — i.e. it behaves like `unlisted` until
the operator opts into listing/indexing. Use `protected`/`private` + auth to
restrict fetching.

Visibility is an override (last-wins), but a child cannot be *more* public than a
`private` ancestor when that would leak content: see the ancestor guard in §5.

## 4. Expiry resolution (SPEC §11)

Three inputs may each appear at any level:
- `expires_at` — absolute instant.
- `delete_after_creation: <dur>` — relative to the object's `created_at`.
- `delete_after_last_access: <dur>` — relative to `last_access_at` (or creation
  if never accessed).

Effective absolute expiry for an object =
`min(` all applicable resolved instants from the object **and every ancestor
level** `)` — **the earliest effective expiry in the tree always wins**
(operator decision C3). There is **no inheritance override**: a child can never
outlive its container.

Concretely, container deletion is authoritative: if a folder expires at T1 and a
file inside it has `expires_at` T2 > T1, the file is deleted at T1 together with
the folder. (We deliberately do not make this configurable — an override would
force a container to linger until its longest-lived child, an edge case we avoid
entirely.)

Durations parse Go-style plus `d`/`w` (e.g. `30d`, `48h`, `2w`). The computed
absolute `expires_at` is written to the denormalized column so the janitor can
query it directly; `delete_after_last_access` makes it a moving target, so it is
recomputed when `last_access_at` advances (throttled — see [08](08-expiry-janitor.md)).
A node's effective `expires_at` is `min(own, parent.effective)`, so the
earliest-wins rule holds transitively without re-walking the whole tree.

## 5. Policy evaluation

`policy.Decide(effective, request) → Decision` returns one of:
`Serve`, `Unauthorized(401)`, `Forbidden(403)`, `NotFound(404)`.

Algorithm for public serve routes:
1. If object/effective is `private` → **404** (never reveal existence, SPEC §17).
2. If expired → **404** (SPEC §11, not 403).
3. If there is effective `access.auth` (object is `protected`, or any visibility
   with auth configured):
   - If a presented credential satisfies any entry (any-of) → continue.
   - Otherwise **challenge by auth type** (operator decision C4). When multiple
     auth types are configured, precedence is **oidc → basic → bearer**:
     - **oidc**: browser request (`Accept: text/html`) → **302 redirect** to the
       OIDC authorization endpoint; non-browser/API request → **401**
       `WWW-Authenticate: Bearer` (so JWT clients can retry). A presented but
       invalid/expired token → 401.
     - **basic** ("htaccess"): → **401** `WWW-Authenticate: Basic realm=...` so
       the browser shows its login dialog. Bad credentials → 401 (re-prompt).
     - **bearer** (only bearer configured): → **404**, hiding existence from
       unauthenticated callers; a presented-but-wrong token → 404 as well.
4. If `public`/`unlisted` and no auth configured → continue.
5. Directory request (`.../` path):
   - allowed only if `access.directory_listing` is true **and** visibility
     permits; otherwise 404 (listing off) — but a known file under it still
     serves.

The per-type challenge means a bearer-protected object is indistinguishable from
a missing one (capability-style), while basic/OIDC objects are intentionally
discoverable so the login flow can run. See [14](14-access-control.md).

**Ancestor guard:** when resolving, if any ancestor is `private`, the object is
treated as private regardless of a more-public child override on public routes
(prevents a public file under a private collection from leaking). A child may be
*more* restrictive than its parent freely. This guard is applied after merge,
before the decision.

## 6. Header safety

`content.headers` is filtered before emission. Rejected (hop-by-hop / unsafe,
SPEC §7.2): `connection`, `transfer-encoding`, `content-length`, `host`,
`keep-alive`, `upgrade`, `te`, `trailer`, `proxy-authenticate`,
`proxy-authorization`, and `set-cookie` (an artifact must not plant cookies on
the shared admin/artifact origin — artifactsd-c63). `content-type` from headers
is ignored in favor of the resolved `content.content_type`. Setting these via
API/CLI is rejected at write time with a 400, and re-filtered at serve time as
defense in depth.

Author-supplied `content-security-policy` is **allowed**: the server emits its
own sandbox CSP as a separate header (§ below), and because browsers intersect
`sandbox` policies an author CSP can only add restrictions, never relax the
sandbox.

### Artifact sandboxing (artifactsd-c63)

Active artifact responses are served with `Content-Security-Policy: sandbox
allow-scripts allow-downloads` by default, giving the artifact an opaque origin
so its JavaScript cannot drive or read the same-origin admin UI. The default is
`content.sandbox` (resolved like any other field; server default from
`ARTIFACTSD_DEFAULT_SANDBOX`, true). Set `content.sandbox: false` only for
trusted content that needs its real origin back (e.g. an interactive SPA that
must reach its own-origin storage or a credentialed/allow-listed third-party
API) — note this is settable by whoever writes the object, so deployments that
accept untrusted uploads should use the separate-admin-origin layout
([14 §7](14-access-control.md)) rather than relying on per-object opt-out.

## 7. Crawler/indexing headers at serve time

- `crawl.indexable: false` → emit `X-Robots-Tag: noindex` (plus any configured
  `crawl.robots.x_robots_tag` values).
- These are independent of access (SPEC §10): a `public` object can still be
  non-indexable.

See [05](05-serving-crawl.md) for robots.txt and sitemap generation, which read
the same resolved `crawl`/`access` data.
