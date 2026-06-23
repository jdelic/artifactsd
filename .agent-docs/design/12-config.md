# 12 — Configuration

Implements SPEC §19. Config is YAML, overlaid by env vars (`ARTIFACTD_*`) and
CLI flags (flags > env > file > built-in defaults).

## 1. File

```yaml
server:
  listen: ":443"
  base_url: "https://artifacts.example.com"
  trust_proxy: false              # honor X-Forwarded-* only if true
  tls:
    cert_file: "./cert.pem"
    key_file: "./key.pem"
  # acme:                          # post-MVP
  #   enabled: false
  #   email: ops@example.com
  #   cache_dir: ./acme

storage:
  data_dir: "./artifactd-data"
  auto_migrate: true

admin:
  enabled: true
  path: "/admin"
  auth:
    type: basic                    # basic (MVP) | oidc (M2)
    username: admin
    password_hash: "$argon2id$..."

defaults:                          # base layer for metadata resolution (03 §2)
  access:
    visibility: private
    directory_listing: false
  crawl:
    crawlable: false
    indexable: false
  expiry:
    delete_after_creation: 30d

limits:
  max_upload_size: 1073741824      # 1 GiB
  max_file_count_per_collection: 10000
  auth_failures:
    max: 10
    window: 1m

janitor:
  interval: 60s
  batch_size: 500
  blob_gc_every: 30
  access_update_throttle: 60s

logging:
  level: info                      # debug|info|warn|error
  format: text                     # text|json
  access_log: true
  access_log_retention: 30d

upload_sessions:
  ttl: 1h
```

## 2. Precedence & validation

- Load file → apply env overrides → apply flags. Unknown keys warn (not fatal)
  to ease forward-compat.
- Validate on startup: `base_url` is absolute; data dir exists/creatable and
  writable; TLS files present if `listen` is TLS and no ACME; admin auth
  configured if admin enabled; durations/sizes parse; `defaults` is a valid
  metadata document.
- Fail fast with a clear message listing all problems.

## 3. Flags (server)

```
artifactd serve   --config <file> --listen --base-url --data --tls-cert --tls-key
                  --admin-user --admin-password-hash --log-level --log-format
artifactd migrate --config <file>            # run migrations only
artifactd hash-password                       # helper to produce argon2id hash
artifactd mcp     --config <file>            # run MCP adapter
artifactd version
```

The SPEC §1 quickstart `artifactd serve --listen :443 --data ./artifactd-data
--base-url https://artifacts.example.com` is satisfied by flag overrides without
a config file (built-in defaults fill the rest).

## 4. Secrets in config

- `admin.password_hash` and any token hashes are hashed values, safe to store.
- Cleartext admin password may be supplied once via `artifactd hash-password` to
  generate the hash, or via `ARTIFACTD_ADMIN_PASSWORD` env (hashed at startup,
  never persisted) for container ergonomics.
- API tokens are created at runtime via the API/CLI, not the config file.

## 5. Defaults rationale

Defaults bias to **safe/private** (SPEC §19 example: `visibility: private`,
crawl off), so a freshly created object is not accidentally public; operators
opt into exposure explicitly per collection/file. This matches the
existence-non-disclosure posture in [06 §4](06-auth-security.md).
