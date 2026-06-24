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

| Concern | Env |
|---------|-----|
| OIDC issuer | `ARTIFACTSD_OIDC_ISSUER` |
| OIDC client id / secret | `ARTIFACTSD_OIDC_CLIENT_ID` / `..._CLIENT_SECRET` |
| OIDC redirect URL | `ARTIFACTSD_OIDC_REDIRECT_URL` |
| Admin group claim | `ARTIFACTSD_ADMIN_GROUP_CLAIM` (default `groups`) |
| Required admin group | `ARTIFACTSD_ADMIN_GROUP` |
| Basic admin fallback | `ARTIFACTSD_ADMIN_BASIC_USER`, `ARTIFACTSD_ADMIN_BASIC_PASSWORD` (or `_HASH`) |
| Admin UI path | `ARTIFACTSD_ADMIN_PATH` (default `/admin`) |

Admin access is granted only when the OIDC token carries the required group
(or via the basic fallback). Secrets are read from env; a cleartext basic
password is hashed at startup and never persisted.

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

## 3. Default object policy (server defaults layer, [03 §2](03-metadata-policy.md))

These set the base layer of metadata resolution. Per-object metadata still
overrides them via the API/CLI.

| Concern | Env | Default |
|---------|-----|---------|
| Default visibility | `ARTIFACTSD_DEFAULT_VISIBILITY` | `public` (decision C5) |
| Default directory listing | `ARTIFACTSD_DEFAULT_DIRECTORY_LISTING` | `false` |
| Default crawlable | `ARTIFACTSD_DEFAULT_CRAWLABLE` | `false` |
| Default indexable | `ARTIFACTSD_DEFAULT_INDEXABLE` | `false` |
| Default expiry | `ARTIFACTSD_DEFAULT_DELETE_AFTER_CREATION` | (unset) |

Per-object robots blocks and headers are richer than env can express; they are
set through the metadata API/CLI, not server config.

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
