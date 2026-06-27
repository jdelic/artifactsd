# 14 — Access control (consolidated)

Access control is the core of artifactsd, so it gets its own document
(operator decision G: "put real thought into user access options and
configuration"). This consolidates and is authoritative over the auth snippets
in [03](03-metadata-policy.md) and [06](06-auth-security.md).

## 1. Principles

- **Public by default, opt into restriction.** Objects require no auth unless
  `access.auth` is configured (decision C5). Visibility defaults to `public`
  with listing/crawl/index off (effectively unlisted-by-URL).
- **Three independent, composable object-auth mechanisms**, evaluated **any-of**:
  bearer token, basic ("htaccess"), and OIDC. A request that satisfies *any*
  configured entry is allowed.
- **Capability-style sharing** via unguessable share URLs and upload URLs that
  need no additional auth.
- **Admin is always OIDC-gated** (decision A1/D1), separate from object auth.
- **12factor**: all server-level auth configuration comes from flags/env, never
  a committed file ([12](12-config.md)).

## 2. Object auth mechanisms

Configured per object in `access.auth` (inherited with **replace** semantics,
decision C1; multiple entries are **any-of**, decision C2).

### 2.1 Bearer token
```yaml
- type: bearer
  token_hash: <sha256-hex>     # API/CLI accepts cleartext `token`, stores hash
```
Request presents `Authorization: Bearer <t>`; server compares `sha256(t)`
(constant-time) against the effective list. High entropy → SHA-256 is the right
hash (fast, indexable; decision D3). **Unauthenticated/invalid → 404** (hides
existence; bearer is a capability).

### 2.2 Basic / htaccess
```yaml
- type: basic
  username: demo
  password_hash: <argon2id>    # decision D3
```
Request presents `Authorization: Basic`; argon2id verify (constant-time, dummy
verify on username miss). **Unauthenticated/invalid → 401
`WWW-Authenticate: Basic`** so browsers show the native login dialog.

### 2.3 OIDC
```yaml
- type: oidc
  issuer: https://accounts.example.com
  audience: artifactsd                    # REQUIRED: expected aud claim
  allowed_subjects: [user@example.com]   # whitelist by sub/email
  allowed_groups: [staff]                # whitelist by group claim
  group_claim: groups                    # which claim holds groups (default: groups)
  any_authenticated: false               # true => any valid token from issuer
  # insecure_skip_audience_check: true   # opt-out; accepts ANY aud at the issuer
```
Authorization is granted if the validated token satisfies **any** of:
`sub`/email ∈ `allowed_subjects`, a group in the `group_claim` ∈
`allowed_groups`, or `any_authenticated: true`. Token validation: fetch issuer
JWKS (cached), verify signature, `exp`, `aud`. `audience` is **required** so a
token minted for a different client at the same issuer cannot satisfy the entry;
write-time validation rejects an OIDC entry that has neither `audience` nor the
explicit `insecure_skip_audience_check: true` opt-out (artifactsd-bhq).
Challenge behavior:
- **Browser** (`Accept: text/html`) → **302** to the issuer's authorization
  endpoint (Authorization Code + PKCE), callback at `/auth/oidc/callback`,
  result stored in a session cookie.
- **API client** → **401 `WWW-Authenticate: Bearer`**; the client presents a
  JWT `Authorization: Bearer <jwt>` it obtained out-of-band.

## 3. Challenge precedence (any-of, mixed types)

When several auth types are configured and the caller is unauthenticated, the
server must choose one challenge. Precedence **oidc → basic → bearer**:

| Configured types | Browser (`text/html`) | API/non-browser |
|------------------|-----------------------|-----------------|
| bearer only | **404** | **404** |
| basic (± bearer) | 401 Basic | 401 Basic |
| oidc (± others) | 302 → issuer | 401 Bearer |

Rationale (decision C4): bearer-only objects stay invisible to unauthenticated
clients (capability); basic/OIDC objects are intentionally discoverable so the
interactive login can proceed. `private` and expired objects always → 404
regardless of configured auth.

## 4. Share URLs

`/s/{share_id}/...` — `share_id` is ≥95 bits from a CSPRNG, unguessable, *is* the
capability. A share carries its own access/expiry/listing overrides (highest
precedence layer). A share may itself be `protected` (add `access.auth`) for
defense in depth, or left open as a pure capability link.

## 5. Upload URLs (capability, decision D2)

`POST .../uploads` returns an `upload_url` containing an unguessable `upload_id`.
`PUT` to it needs **no additional auth** — possession of the URL is the
capability, so agents can be handed the URL and upload directly (SPEC §16 flow).
Safeguards: high-entropy id, short configurable expiry, single-use (consumed on
completion), size limits. See [07](07-storage-uploads.md).

## 6. Management API auth (decision D1)

- `Authorization: Bearer tok_...` API tokens, minted by an admin, carrying
  **scopes**: `admin`, `collections:read`, `collections:write`, `shares:write`,
  `tokens:write`, `maintenance`. Stored as SHA-256.
- Scope enforcement per endpoint ([04](04-http-api.md)).
- Tokens are created at runtime (API/CLI/admin UI), never via config.

## 7. Admin authentication & authorization (decisions A1, D1)

- **Admin UI is OIDC-gated** and additionally may use basic/htaccess.
- **Authorization for admin is by group claim, in three modes** keyed on
  `ARTIFACTSD_ADMIN_GROUP`: *unset* → fail closed (OIDC admin disabled, nobody
  authorized); `*` → any authenticated user is an admin (explicit opt-out of
  group gating, safe only behind a trusted issuer); any other value → require
  that group. Empty is fail-closed by design so a forgotten variable never opens
  admin to the whole IdP. Configured via **environment variables**:
  - `ARTIFACTSD_OIDC_ISSUER`, `ARTIFACTSD_OIDC_CLIENT_ID`,
    `ARTIFACTSD_OIDC_CLIENT_SECRET`
  - `ARTIFACTSD_OIDC_REDIRECT_URL` — the full callback URL the provider
    redirects back to (sent verbatim as `redirect_uri`, must be whitelisted at
    the provider). Defaults to `<base_url>/auth/oidc/callback`; see
    [12 §2](12-config.md) for the full explanation and when to override.
  - `ARTIFACTSD_ADMIN_GROUP_CLAIM` (default `groups`)
  - `ARTIFACTSD_ADMIN_GROUP` (unset → fail closed · `*` → any authenticated · else require that group)
  - optional `ARTIFACTSD_ADMIN_BASIC_USER` / `ARTIFACTSD_ADMIN_BASIC_PASSWORD`
    (or `_HASH`) for an htaccess fallback admin login.
- Successful admin login → secure session cookie (HttpOnly, SameSite, Secure
  under TLS). All admin actions audited.
- Admins mint API bearer tokens for programmatic/agent use.

## 8. What lives where

| Concern | Source |
|---------|--------|
| Object/folder/collection/share auth | per-object metadata (API/CLI/admin) |
| Default visibility & policy | server defaults (flags/env) |
| Admin OIDC/basic config | env vars (§7) |
| API tokens & scopes | runtime, DB (hashed) |
| Upload/share capabilities | runtime, DB (unguessable ids) |

## 9. Test focus

Table-driven tests for: any-of evaluation across all three types; challenge
precedence × browser/API matrix; bearer→404 vs basic→401 vs oidc→redirect;
OIDC group/subject/any-authenticated whitelisting; admin group-claim gating;
share/upload capability (no token) + expiry; private/expired→404.

## 10. Browser isolation of active artifacts (artifactsd-c63)

Artifacts are intentionally allowed to be active browser content (HTML reports,
SPAs, SVG). By default they are served from `/c` and `/s` on the **same origin**
as the cookie-authenticated admin UI, so without isolation a malicious artifact
opened by an admin could `fetch("/admin/…")`, read the same-origin CSRF token,
mint an admin API token, and exfiltrate it — same-origin script defeats CSRF,
SameSite, and HttpOnly.

**Primary boundary — sandbox the artifact (the deployment-simple default).**
Active artifact responses carry `Content-Security-Policy: sandbox allow-scripts
allow-downloads` (no `allow-same-origin`), so the artifact runs in an *opaque*
origin and cannot read/drive the admin origin. This is on by default
(`ARTIFACTSD_DEFAULT_SANDBOX`, per-object `content.sandbox`). Server-emitted as
its own CSP header so author CSPs only tighten it; `Set-Cookie` is stripped from
artifact metadata. Tradeoff: a sandboxed page has a `null` origin, so it loses
its own-origin storage/cookies and presents `Origin: null` to third-party APIs
(only `*`-CORS APIs work) — trusted interactive content opts out with
`content.sandbox: false`, regaining the real origin.

**Defense in depth on the admin side.** Every admin response sets
`X-Frame-Options: DENY`, `Content-Security-Policy: frame-ancestors 'none'`,
`X-Content-Type-Options: nosniff`, `Referrer-Policy: same-origin`, and
`Cache-Control: no-store`; mutating admin requests labeled `Sec-Fetch-Site:
cross-site` are rejected (on top of the existing same-origin + CSRF-token check).

**Stronger boundary — separate admin origin (optional, recommended at scale).**
artifactsd supports **both** topologies:

- *Single domain (default, easy):* admin and artifacts share one origin; the
  sandbox is the boundary. Good for simple deployments and trusted content.
- *Separate admin origin (hardened):* host the cookie-authenticated admin UI
  (and its `/auth/oidc/callback`) on its own origin, e.g. `admin.example.com`,
  while artifacts stay on the public origin. The admin session cookie is then
  scoped to a host artifact JS cannot reach, so artifact→admin theft is
  prevented *structurally* — independent of the sandbox. In this mode
  `content.sandbox: false` is safe (it hands the artifact only the public
  origin, where no admin credentials live), making it the right home for rich
  interactive SPAs that need a stable, allow-listable origin. The management
  API is bearer-token (no ambient cookie) so it is not same-origin-exploitable;
  it may ride with admin or stay public. *(Host-based routing for this mode is
  tracked as a follow-up; the single-domain sandbox ships first.)*
