# 12 — Configuration (12factor)

Operator decision F4/G: **configuration is command-line flags and environment
variables only** — no committed config file. This follows the
[12factor](https://12factor.net) app methodology: config lives in the
environment, logs are event streams, the process is stateless apart from its
declared backing store (local SQLite + blobs, decision B3).

Precedence: **flags > env > built-in defaults**. Every flag has a matching
`ARTIFACTSD_`-prefixed env var; flags exist mainly for interactive/dev use,
env for deployment.

## 1. Server settings

| Concern | Flag | Env | Default |
|---------|------|-----|---------|
| Listen address | `--listen` | `ARTIFACTSD_LISTEN` | `:8080` |
| Public base URL | `--base-url` | `ARTIFACTSD_BASE_URL` | (required) |
| Data directory | `--data` | `ARTIFACTSD_DATA_DIR` | `./artifactsd-data` |
| Auto-migrate on boot | `--auto-migrate` | `ARTIFACTSD_AUTO_MIGRATE` | `true` |
| Trust proxy headers | `--trust-proxy` | `ARTIFACTSD_TRUST_PROXY` | `false` |
| Log level | `--log-level` | `ARTIFACTSD_LOG_LEVEL` | `info` |

### TLS (decision D4)
| Concern | Flag | Env | Default |
|---------|------|-----|---------|
| TLS mode | `--tls` | `ARTIFACTSD_TLS` | `off` \| `files` \| `acme` |
| Cert file | `--tls-cert` | `ARTIFACTSD_TLS_CERT_FILE` | |
| Key file | `--tls-key` | `ARTIFACTSD_TLS_KEY_FILE` | |
| ACME domains | `--acme-domains` | `ARTIFACTSD_ACME_DOMAINS` | |
| ACME email | `--acme-email` | `ARTIFACTSD_ACME_EMAIL` | |
| ACME cache dir | `--acme-cache` | `ARTIFACTSD_ACME_CACHE_DIR` | `<data>/acme` |

`--tls=acme` enables automatic Let's Encrypt certificates from the first release.

## 2. Admin & OIDC (decisions A1, D1; see [14](14-access-control.md))

| Concern | Env | Default |
|---------|-----|---------|
| OIDC issuer | `ARTIFACTSD_OIDC_ISSUER` | |
| OIDC client id / secret | `ARTIFACTSD_OIDC_CLIENT_ID` / `..._CLIENT_SECRET` | |
| OIDC redirect URL | `ARTIFACTSD_OIDC_REDIRECT_URL` | `<base_url>/auth/oidc/callback` |
| Admin group claim | `ARTIFACTSD_ADMIN_GROUP_CLAIM` | `groups` |
| Required admin group | `ARTIFACTSD_ADMIN_GROUP` | |
| Basic admin fallback | `ARTIFACTSD_ADMIN_BASIC_USER`, `ARTIFACTSD_ADMIN_BASIC_PASSWORD` (or `_HASH`) | |
| Admin UI path | `ARTIFACTSD_ADMIN_PATH` | `/admin` |

Admin access is granted only when the OIDC token carries the required group
(or via the basic fallback). Secrets are read from env; a cleartext basic
password is hashed at startup and never persisted.

### The OIDC redirect URL

`ARTIFACTSD_OIDC_REDIRECT_URL` is the **full, absolute URL** the OIDC provider
sends the browser back to after login. artifactsd passes it verbatim to the
provider as the OAuth2 `redirect_uri` in both the authorization request and the
token exchange, so two things must hold:

1. It must resolve to artifactsd's callback route, **`/auth/oidc/callback`**.
2. The **same exact URL must be registered as an allowed redirect URI at your
   OIDC provider** (most providers reject any `redirect_uri` not on that list).

You normally **do not set it**: when left unset it defaults to
`<base_url>/auth/oidc/callback` (e.g. base URL `https://artifacts.example.com`
→ `https://artifacts.example.com/auth/oidc/callback`). So the only thing you
have to do at the provider is whitelist that one URL. Override it only when
artifactsd's callback is reached at a different external URL than `base_url` —
for example behind a proxy that terminates on another hostname, or a
separate-admin-origin deployment ([14 §7/§10](14-access-control.md)). When set,
it is validated to be an absolute `http(s)` URL at startup.

### 2.1 Bootstrap admin token

| Concern | Env |
|---------|-----|
| Initial admin API token | `ARTIFACTSD_BOOTSTRAP_TOKEN` |

The management API (`/api/v1/tokens` etc.) requires an existing admin-scoped
token to mint any further tokens, so a fresh deployment otherwise has no way
in. If `ARTIFACTSD_BOOTSTRAP_TOKEN` is set, `serve` seeds it as an
admin-scoped API token on every boot — idempotently, keyed on the token's
hash, so restarting with the same value does not create duplicate rows. Treat
it like any other secret (a deployment-time env var, not a default); once a
"real" admin token exists, operators can drop it from the environment.

Unlike every other token, the bootstrap value carries **no `tok_` prefix
requirement** — it's an operator-chosen secret, not a generated one. The auth
middleware ([06 §1.1](06-auth-security.md)) normally only recognizes Bearer
tokens prefixed `tok_`, so `Authenticator.WithBootstrapToken` special-cases an
exact match against the configured bootstrap value and normalizes it to
`tok_`+token before the usual hash lookup, matching how it was seeded
(`cmd/artifactsd/bootstrap.go`). Present it as `Bearer <ARTIFACTSD_BOOTSTRAP_TOKEN value>`,
with no prefix added by the operator.

## 3. Default object policy (server defaults layer, [03 §2](03-metadata-policy.md))

These set the base layer of metadata resolution. Per-object metadata still
overrides them via the API/CLI.

| Concern | Env | Default |
|---------|-----|---------|
| Default visibility | `ARTIFACTSD_DEFAULT_VISIBILITY` | `public` (decision C5) |
| Default directory listing | `ARTIFACTSD_DEFAULT_DIRECTORY_LISTING` | `false` |
| Default crawlable | `ARTIFACTSD_DEFAULT_CRAWLABLE` | `false` |
| Default indexable | `ARTIFACTSD_DEFAULT_INDEXABLE` | `false` |
| Default sandbox | `ARTIFACTSD_DEFAULT_SANDBOX` | `true` |
| Default expiry | `ARTIFACTSD_DEFAULT_DELETE_AFTER_CREATION` | (unset) |

Per-object robots blocks and headers are richer than env can express; they are
set through the metadata API/CLI, not server config.

`ARTIFACTSD_DEFAULT_DELETE_AFTER_CREATION` is a creation-relative default object
expiry (e.g. `30d`). When set, it is applied during denormalization to any
collection or node whose own metadata sets no expiry, so new content expires by
default; an explicit per-object expiry always wins over it (artifactsd-8yy).

## 4. Limits, janitor, uploads

| Concern | Env | Default |
|---------|-----|---------|
| Max upload size | `ARTIFACTSD_MAX_UPLOAD_SIZE` | `1073741824` (1 GiB) |
| Max files / collection | `ARTIFACTSD_MAX_FILES_PER_COLLECTION` | `10000` |
| Auth-failure limit / window | `ARTIFACTSD_AUTH_FAIL_MAX` / `..._WINDOW` | `10` / `1m` |
| Janitor interval | `ARTIFACTSD_JANITOR_INTERVAL` | `60s` |
| Janitor batch size | `ARTIFACTSD_JANITOR_BATCH_SIZE` | `500` |
| Last-access throttle | `ARTIFACTSD_ACCESS_UPDATE_THROTTLE` | `60s` |
| Upload session TTL | `ARTIFACTSD_UPLOAD_TTL` | `1h` |

## 5. Logging (decision F5)

- **Access logs → stdout**, structured (one event per request).
- **App logs + errors → stderr**, via `log/slog`.
- `--log-level` / `ARTIFACTSD_LOG_LEVEL` controls stderr verbosity.
- No log files, no DB-backed access log; aggregation is the deployer's job
  (containers/journald capture stdout/stderr).

## 6. Subcommands

```
artifactsd serve            # run the server (reads flags/env above)
artifactsd migrate          # apply migrations only
artifactsd hash-password    # produce an argon2id hash for the basic fallback
artifactsd mcp              # run the MCP adapter (see 11)
artifactsd version
```

The SPEC §1 quickstart works as
`artifactsd serve --listen :443 --base-url https://artifacts.example.com
--data ./artifactsd-data` with everything else from defaults/env.

### 6.1 `artifactsd mcp` settings

`mcp` carries none of the storage/TLS/admin settings above — it is a thin
HTTP-to-HTTP bridge, with its own listen address, calling the artifactsd
management API as a client (design/11 §1):

| Concern | Flag | Env | Default |
|---------|------|-----|---------|
| MCP HTTP listen address | `--listen` | `ARTIFACTSD_MCP_LISTEN` | `:8081` |
| artifactsd API base URL to call | `--base-url` | `ARTIFACTSD_BASE_URL` | (required) |
| Management API token | `--token` | `ARTIFACTSD_MCP_TOKEN` | (required) |
| Log level | `--log-level` | `ARTIFACTSD_LOG_LEVEL` | `info` |

`--base-url`/`ARTIFACTSD_BASE_URL` is shared with `serve` by name since it
means the same thing either way — "where is the artifactsd server" — just
consumed differently (`serve` uses it to build links about itself; `mcp` uses
it as the apiclient target). The listen address gets its own default port so
both can run as separate processes on one host without colliding.

## 7. Validation

On startup, validate and **fail fast** with all problems listed: `base_url`
absolute; data dir creatable/writable; TLS inputs consistent with `--tls` mode;
OIDC config present if admin enabled (issuer/client/redirect) or a basic
fallback configured; durations/sizes parse; default visibility is a valid value.

## 8. Why no config file

A single source of truth (env) avoids file/flag/env precedence confusion, plays
well with containers and secret managers, and keeps secrets out of the repo.
Operators who want a file can use a `.env`/dotenv loader or their orchestrator's
env injection — artifactsd itself only reads flags and the process environment.
