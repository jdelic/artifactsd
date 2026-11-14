# Configuration reference

The **server** (`artifactsd serve` / `mcp`) is configured with command-line
flags and `ARTIFACTSD_*` environment variables only — there is **no config
file** (12factor). Precedence is **flags > environment > built-in defaults**.
Every flag has a matching env var; flags are convenient for interactive/dev
use, env for deployment.

> The **`artifactctl` client** has its own small config file for convenience —
> that's documented separately in the [CLI reference](cli.md#configuration).

On startup the server validates everything and **fails fast**, listing all
problems at once (base URL must be absolute, data dir must be writable, TLS
inputs must match the chosen mode, OIDC/basic must be present if the admin UI
is enabled, durations/sizes must parse).

## Server

| Setting | Flag | Env | Default |
|---------|------|-----|---------|
| Listen address | `--listen` | `ARTIFACTSD_LISTEN` | `:8080` |
| Public base URL | `--base-url` | `ARTIFACTSD_BASE_URL` | **(required)** |
| Admin host (separate origin) | `--admin-host` | `ARTIFACTSD_ADMIN_HOST` | (unset → single domain) |
| Data directory | `--data` | `ARTIFACTSD_DATA_DIR` | `./artifactsd-data` |
| Auto-migrate on boot | `--auto-migrate` | `ARTIFACTSD_AUTO_MIGRATE` | `true` |
| Trusted proxies | `--trusted-proxies` | `ARTIFACTSD_TRUSTED_PROXIES` | (none → forwarded headers ignored) |
| Trust proxy headers (**deprecated**) | `--trust-proxy` | `ARTIFACTSD_TRUST_PROXY` | `false` |
| Log level | `--log-level` | `ARTIFACTSD_LOG_LEVEL` | `info` |

`--base-url` is the public origin used to build links about the server (`/c`,
`/s`, upload URLs); it must be an absolute URL.

### Reverse proxies

`--trusted-proxies` is a comma-separated list of the addresses (CIDRs or single
IPs, IPv4 or IPv6) of the reverse proxies in front of artifactsd, e.g.
`ARTIFACTSD_TRUSTED_PROXIES=10.0.0.5,fd00::/64`. `X-Forwarded-For`,
`X-Forwarded-Host` and `X-Forwarded-Proto` are honored **only** on requests
whose TCP peer is in that list; from anyone else they are ignored, so a client
that reaches the listener directly cannot forge its address, scheme or host.

The client address is taken from `X-Forwarded-For` read **right to left**:
artifactsd skips entries that are trusted proxies and uses the first one that
is not. Entries further left were supplied by the client and are never used.
Your proxy may either overwrite or append to `X-Forwarded-For`; both work. List
every proxy hop that sits between the internet and artifactsd. The effective
list is logged at startup.

`--trust-proxy=true` is **deprecated**. It still works and means "trust
loopback and the private ranges" (`127.0.0.0/8, ::1/128, 10.0.0.0/8,
172.16.0.0/12, 192.168.0.0/16, fc00::/7`), and logs a warning at startup. If
`--trusted-proxies` is also set, the explicit list wins.

### Admin origin: single-domain vs. separate host

By default the admin UI and public artifacts share one origin, and a malicious
artifact is contained by the per-object sandbox (`ARTIFACTSD_DEFAULT_SANDBOX`).
For a stronger, structural boundary, set **`ARTIFACTSD_ADMIN_HOST`** to a
separate bare host (e.g. `admin.example.com`, distinct from the base-URL host):

- The admin UI and its OIDC login/callback are then served **only** on the admin
  host; public artifacts (`/c`, `/s`, `robots.txt`, `sitemap.xml`) **only** on
  the base-URL host. Requests to the wrong plane get `404`.
- The admin session cookie is host-only, so it is never sent to the public
  origin — artifact JavaScript there cannot reach it even if you disable the
  sandbox (`content.sandbox: false`, which only admin-scoped tokens may set),
  which makes this the right setup for rich
  interactive SPAs that need a stable origin.
- Point both hostnames at artifactsd (same process/listener). If a reverse proxy
  terminates them, list it in `--trusted-proxies` so the admin/public split
  honors `X-Forwarded-Host`, or have the proxy forward the original `Host`.
- Whitelist `https://<admin-host>/auth/oidc/callback` at your IdP — that is the
  redirect URL artifactsd defaults to in this mode.
- The bearer-token management API (`/api/v1`) and health checks have no ambient
  cookie, so they stay reachable on both hosts.

See [design/14 §10](../.agent-docs/design/14-access-control.md) for the threat
model behind this.

## TLS

| Setting | Flag | Env | Default |
|---------|------|-----|---------|
| TLS mode | `--tls` | `ARTIFACTSD_TLS` | `off` |
| Cert file | `--tls-cert` | `ARTIFACTSD_TLS_CERT_FILE` | |
| Key file | `--tls-key` | `ARTIFACTSD_TLS_KEY_FILE` | |
| ACME domains | `--acme-domains` | `ARTIFACTSD_ACME_DOMAINS` | |
| ACME email | `--acme-email` | `ARTIFACTSD_ACME_EMAIL` | |
| ACME cache dir | `--acme-cache` | `ARTIFACTSD_ACME_CACHE_DIR` | `<data>/acme` |

- `off` — plain HTTP (terminate TLS at a reverse proxy in front).
- `files` — serve `--tls-cert` / `--tls-key`.
- `acme` — automatic Let's Encrypt certificates for `--acme-domains`, cached on
  disk. Needs the server reachable on port 443.

## Admin UI & OIDC

The admin UI is gated by OIDC, with an optional Basic Auth fallback. Admin
access is granted to an authenticated user who is on the subject/email allowlist
**or** carries the required group (or via the basic fallback). Secrets come from
the environment; a cleartext basic password is hashed at startup and never
persisted.

| Setting | Env | Default |
|---------|-----|---------|
| Admin UI path | `ARTIFACTSD_ADMIN_PATH` | `/admin` |
| OIDC issuer | `ARTIFACTSD_OIDC_ISSUER` | |
| OIDC client id | `ARTIFACTSD_OIDC_CLIENT_ID` | |
| OIDC client secret | `ARTIFACTSD_OIDC_CLIENT_SECRET` | |
| OIDC redirect URL | `ARTIFACTSD_OIDC_REDIRECT_URL` | `<base_url>/auth/oidc/callback` |
| OIDC scopes | `ARTIFACTSD_OIDC_SCOPES` | `openid email profile` |
| Admin subject/email allowlist | `ARTIFACTSD_ADMIN_SUBJECTS` | |
| Admin group claim | `ARTIFACTSD_ADMIN_GROUP_CLAIM` | `groups` |
| Required admin group | `ARTIFACTSD_ADMIN_GROUP` | |
| Basic fallback user | `ARTIFACTSD_ADMIN_BASIC_USER` | |
| Basic fallback password | `ARTIFACTSD_ADMIN_BASIC_PASSWORD` (or `..._HASH`) | |

Use `artifactsd hash-password` to produce an argon2id hash for
`ARTIFACTSD_ADMIN_BASIC_PASSWORD_HASH`. Hashes must stay within 8–128 MiB of
memory, 1–6 passes and 1–8 threads (the same bounds apply to `password_hash` in
object metadata); the server refuses to start otherwise.

### OIDC-protected objects: allowed issuers

| Setting | Flag | Env | Default |
|---------|------|-----|---------|
| Allowed object-OIDC issuers | `--oidc-allowed-issuers` | `ARTIFACTSD_OIDC_ALLOWED_ISSUERS` | (none) |

An `oidc` entry in an object's `access.auth` makes the **server** fetch the
issuer's discovery document and keys, so only issuers on this allowlist may be
used. The effective list is `ARTIFACTSD_OIDC_ALLOWED_ISSUERS` plus your own
`ARTIFACTSD_OIDC_ISSUER` (if set); when both are empty, object OIDC is
disabled. Entries are comma-separated absolute `https` URLs (`http` is accepted
only for loopback hosts, for development) and are matched exactly, ignoring one
trailing slash.

Writing metadata that names any other issuer is rejected with `400`. Objects
stored earlier with a non-allowed issuer stay auth-required and simply cannot be
unlocked through that entry; no request is ever sent to such an issuer. A failed
discovery is remembered for 60 seconds, discovery and key fetches time out after
10 seconds, and redirects to another host are refused.

### Registering artifactsd at your IdP

artifactsd is an OIDC **confidential client** using the **Authorization Code
flow with PKCE (S256)**. When you create the client (application) at your
identity provider, configure it to match what artifactsd expects — a wrong
choice here usually fails opaquely at login time:

- **Flow / response type: Authorization Code (`response_type=code`).** Do **not**
  pick Hybrid or Implicit. artifactsd reads the authorization code from the
  redirect's **query string**; a Hybrid/Implicit client returns `code`/`id_token`
  in the URL **fragment** (`#...`), which the browser never sends to the server —
  the callback then fails with `missing authorization code`. If you see that
  error with no other explanation, the client is almost certainly not a plain
  Authorization Code client.
- **Redirect URI to whitelist:** `<base_url>/auth/oidc/callback` (e.g.
  `https://artifacts.example.com/auth/oidc/callback`). This is the default value
  of `ARTIFACTSD_OIDC_REDIRECT_URL`; register this exact URL as an allowed
  redirect URI. Most providers reject any `redirect_uri` not on the list.
- **Client type: confidential** — a `client_id` **and** `client_secret`. PKCE is
  always sent in addition (`code_challenge_method=S256`); you do not need to
  enable a separate "public/PKCE" client type.
- **Scopes requested:** `openid email profile` by default
  (`ARTIFACTSD_OIDC_SCOPES`). artifactsd does **not** request a `groups` scope:
  it is non-standard and many IdPs reject it with `invalid_scope`. Make sure
  these scopes are allowed for the client.
- **Group claim placement:** artifactsd reads claims (`sub`, `email`, groups)
  **directly from the token-endpoint `id_token`** — it makes **no** userinfo
  call and uses **no** refresh token. If you authorize admins by group, your IdP
  must emit the groups **into the `id_token`**, under the claim named by
  `ARTIFACTSD_ADMIN_GROUP_CLAIM` (default `groups`). Emitting groups only on the
  userinfo endpoint will not work.

#### Choosing how admins are authorized

After a successful login, an identity is an admin if it satisfies **either**
mechanism (any-of):

- **`ARTIFACTSD_ADMIN_SUBJECTS`** — a comma-separated allowlist matched against
  the login's `sub` **or** `email`. This is the **portable** option: `sub` is
  always present and `email` is standard, so it works on **every** IdP even when
  groups are unavailable (e.g. Google Workspace, which does not put group
  membership in the `id_token`). Recommended starting point:
  `ARTIFACTSD_ADMIN_SUBJECTS="you@example.com"`.
- **`ARTIFACTSD_ADMIN_GROUP`** — group-claim gating: *unset* grants nothing via
  groups; `*` admits **any** authenticated user (only safe behind a trusted,
  staff-only IdP); any other value requires that exact group in the
  `ARTIFACTSD_ADMIN_GROUP_CLAIM`.

With both unset, OIDC admin **fails closed** (nobody is authorized). The
effective mode is logged once at startup.

#### Per-IdP notes (where groups come from)

| IdP | Notes |
|-----|-------|
| **Keycloak** | Create a confidential client, Standard Flow (Authorization Code) enabled, Implicit/Hybrid off. Add a **Group Membership** mapper (or a client scope with one) writing to the `groups` claim and tick "Add to ID token". You may need a provider-specific scope listed in `ARTIFACTSD_OIDC_SCOPES` if you gate the mapper behind a client scope. |
| **Authentik** | Create an OAuth2/OIDC Provider (Authorization Code), confidential. Add a Scope Mapping that emits `groups` into the token, and assign it to the application. |
| **Azure AD / Entra ID** | Register an app, Web platform with the redirect URI; add a `client_secret`. Configure the token to include a **groups** claim (Token configuration → add groups claim, ID token). Note Azure may emit group **object IDs** rather than names — set `ARTIFACTSD_ADMIN_GROUP` to the GUID, or just use `ARTIFACTSD_ADMIN_SUBJECTS`. |
| **Google Workspace** | Standard OIDC works for login, but Google does **not** put group membership in the `id_token`. Use `ARTIFACTSD_ADMIN_SUBJECTS` (email allowlist) for admin authorization. |

### Bootstrap admin token

| Setting | Env |
|---------|-----|
| Initial admin API token | `ARTIFACTSD_BOOTSTRAP_TOKEN` |

The management API requires an existing admin-scoped token to mint further
tokens, so a fresh deployment otherwise has no way in. When set,
`ARTIFACTSD_BOOTSTRAP_TOKEN` is seeded as an admin-scoped API token on every
boot — idempotently (keyed on the token hash, so restarts don't create
duplicates). Present it as `Authorization: Bearer <value>` exactly; unlike
generated tokens it carries no `tok_` prefix requirement. Treat it as a secret
and drop it from the environment once a real admin token exists.

## Default object policy

These set the base layer of metadata resolution. Per-object metadata set via
the API/CLI still overrides them.

| Setting | Env | Default |
|---------|-----|---------|
| Default visibility | `ARTIFACTSD_DEFAULT_VISIBILITY` | `public` |
| Default directory listing | `ARTIFACTSD_DEFAULT_DIRECTORY_LISTING` | `false` |
| Default crawlable | `ARTIFACTSD_DEFAULT_CRAWLABLE` | `false` |
| Default indexable | `ARTIFACTSD_DEFAULT_INDEXABLE` | `false` |
| Default expiry | `ARTIFACTSD_DEFAULT_DELETE_AFTER_CREATION` | (unset) |

Richer per-object robots blocks and response headers can't be expressed in env;
set them through the metadata API/CLI.

### Allowed response headers

`content.headers` only accepts an allowlist of response headers, because
whatever a writer stores there is sent on the artifact's responses, on the
same origin as the admin UI unless `ARTIFACTSD_ADMIN_HOST` is set:

`Cache-Control`, `Content-Disposition`, `Content-Language`, `Content-Encoding`,
`Content-Security-Policy`, `Content-Security-Policy-Report-Only`, `X-Robots-Tag`,
`Link`, `Vary`, `Expires`, `Referrer-Policy`, `Permissions-Policy`,
`Cross-Origin-Resource-Policy`, `Cross-Origin-Opener-Policy`,
`Cross-Origin-Embedder-Policy`, `Timing-Allow-Origin`, `X-Content-Type-Options`,
`X-Frame-Options`, every `Access-Control-*` header, and every `X-*` header except
`X-Request-Id`, `X-Forwarded-*`, `X-Accel-*` and `X-Sendfile`.

`Content-Type` is accepted but ignored in favour of `content.content_type`.
Values may not contain CR, LF or NUL; names are capped at 128 bytes, values at
8 KiB, and a document at 32 headers. Anything else is rejected with `400`.

| Setting | Flag | Env | Default |
|---------|------|-----|---------|
| Extra allowed headers | `--extra-allowed-headers` | `ARTIFACTSD_EXTRA_ALLOWED_HEADERS` | (none) |

A comma-separated list of extra header names to allow (e.g. `Server-Timing`).
**Every writer** (any token with `collections:write` or `shares:write`) can then
set them, so only add headers that are harmless on your origin. Hop-by-hop
headers, `Set-Cookie`, `Content-Length` and `Content-Type` can never be added;
the server refuses to start if you list one. Documents stored before an entry
was removed stay readable, and the disallowed headers are simply not served.

## Limits, janitor & uploads

| Setting | Env | Default |
|---------|-----|---------|
| Max upload size (bytes) | `ARTIFACTSD_MAX_UPLOAD_SIZE` | `1073741824` (1 GiB) |
| Max files per collection | `ARTIFACTSD_MAX_FILES_PER_COLLECTION` | `10000` |
| Auth-failure limit | `ARTIFACTSD_AUTH_FAIL_MAX` | `10` |
| Auth-failure window | `ARTIFACTSD_AUTH_FAIL_WINDOW` | `1m` |
| Janitor interval | `ARTIFACTSD_JANITOR_INTERVAL` | `60s` |
| Janitor batch size | `ARTIFACTSD_JANITOR_BATCH_SIZE` | `500` |
| Last-access update throttle | `ARTIFACTSD_ACCESS_UPDATE_THROTTLE` | `60s` |
| Upload session TTL | `ARTIFACTSD_UPLOAD_TTL` | `1h` |

`MAX_FILES_PER_COLLECTION` caps the number of **files** in one collection
(folders do not count). Creating a new file beyond it is rejected with `409`
on every write path (API `PUT`, large uploads, the admin UI and upload links);
replacing an existing file is always allowed. On upload links the check is
atomic with the write; for token-authenticated writers concurrent uploads may
overshoot the cap by a few files.

When more than `AUTH_FAIL_MAX` auth failures occur within `AUTH_FAIL_WINDOW`,
further attempts are rate-limited (`429`). The janitor reaps expired objects and
abandoned upload sessions every `JANITOR_INTERVAL`, `JANITOR_BATCH_SIZE` rows at
a time; trigger a pass on demand with `artifactctl maintenance cleanup`.

## Logging

- **Access logs → stdout**, structured, one event per request.
- **App logs and errors → stderr**, via `log/slog`; verbosity from
  `--log-level`.
- No log files and no DB-backed access log — aggregation is the deployer's job
  (containers/journald capture stdout/stderr).

## MCP adapter

`artifactsd mcp` is a thin HTTP-to-HTTP bridge and carries none of the storage,
TLS, or admin settings; it calls the management API as a client.

| Setting | Flag | Env | Default |
|---------|------|-----|---------|
| MCP listen address | `--listen` | `ARTIFACTSD_MCP_LISTEN` | `:8081` |
| artifactsd API base URL | `--base-url` | `ARTIFACTSD_BASE_URL` | **(required)** |
| Management API token | `--token` | `ARTIFACTSD_MCP_TOKEN` | **(required)** |
| Log level | `--log-level` | `ARTIFACTSD_LOG_LEVEL` | `info` |

`--base-url` means the same thing as for `serve` ("where is the artifactsd
server"), but here it's the target the adapter calls. The separate default port
lets `serve` and `mcp` run as two processes on one host without colliding.
