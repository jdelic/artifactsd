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
| Data directory | `--data` | `ARTIFACTSD_DATA_DIR` | `./artifactsd-data` |
| Auto-migrate on boot | `--auto-migrate` | `ARTIFACTSD_AUTO_MIGRATE` | `true` |
| Trust proxy headers | `--trust-proxy` | `ARTIFACTSD_TRUST_PROXY` | `false` |
| Log level | `--log-level` | `ARTIFACTSD_LOG_LEVEL` | `info` |

`--base-url` is the public origin used to build links about the server (`/c`,
`/s`, upload URLs); it must be an absolute URL. Set `--trust-proxy` only when
running behind a proxy you control, so `X-Forwarded-*` headers are honored.

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
access is granted only when the OIDC token carries the required group (or via
the basic fallback). Secrets come from the environment; a cleartext basic
password is hashed at startup and never persisted.

| Setting | Env | Default |
|---------|-----|---------|
| Admin UI path | `ARTIFACTSD_ADMIN_PATH` | `/admin` |
| OIDC issuer | `ARTIFACTSD_OIDC_ISSUER` | |
| OIDC client id | `ARTIFACTSD_OIDC_CLIENT_ID` | |
| OIDC client secret | `ARTIFACTSD_OIDC_CLIENT_SECRET` | |
| OIDC redirect URL | `ARTIFACTSD_OIDC_REDIRECT_URL` | |
| Admin group claim | `ARTIFACTSD_ADMIN_GROUP_CLAIM` | `groups` |
| Required admin group | `ARTIFACTSD_ADMIN_GROUP` | |
| Basic fallback user | `ARTIFACTSD_ADMIN_BASIC_USER` | |
| Basic fallback password | `ARTIFACTSD_ADMIN_BASIC_PASSWORD` (or `..._HASH`) | |

Use `artifactsd hash-password` to produce an argon2id hash for
`ARTIFACTSD_ADMIN_BASIC_PASSWORD_HASH`.

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
