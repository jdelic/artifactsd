# 06 — Auth & security

Covers SPEC §8 (access auth), §17 (security requirements).

## 1. Authentication mechanisms

Three object-level mechanisms (configured in `access.auth`, evaluated **any-of**)
plus management-API tokens and admin auth.

### 1.1 Bearer (object & management)
- **Object bearer**: `access.auth` entry `{type:bearer, token_hash}`. Incoming
  `Authorization: Bearer <t>` → `sha256(t)` compared (constant-time) to stored
  hashes in the effective auth list.
- **Management/API bearer**: `api_tokens` table. Token string `tok_<base62>`,
  ≥256-bit secret; only `sha256` hash stored, with a `token_prefix` for display.
  Scopes gate endpoints ([04](04-http-api.md)).
- Bearer tokens are high-entropy, so SHA-256 (fast, indexable) is appropriate;
  argon2 is unnecessary and would prevent O(1) lookup for API tokens.

### 1.2 Basic auth
- `access.auth` entry `{type:basic, username, password_hash}` where
  `password_hash` is **argon2id** (SPEC §17, `$argon2id$...`). On request,
  parse `Authorization: Basic`, look up matching username, verify with argon2id
  (constant-time, includes a dummy verify on username miss to avoid timing
  oracles). Admin UI default auth is basic (SPEC §19).

### 1.3 OIDC (in MVP — operator decision A1)
- `{type:oidc, issuer, audience, allowed_subjects, allowed_groups, group_claim,
  any_authenticated}`. Validate a presented `Authorization: Bearer <jwt>` (API)
  or run the Authorization-Code+PKCE browser flow against the issuer's JWKS via
  `coreos/go-oidc`; check signature/`exp`/`aud`, then grant on `sub ∈
  allowed_subjects` **or** a group in `group_claim` ∈ `allowed_groups` **or**
  `any_authenticated`. Full model in [14 — Access control](14-access-control.md).
  OIDC is part of the MVP (moved up from the original M2 default).

### 1.4 Auth abstraction
```go
type Verifier interface {
    // Verify returns (true, subject) if the request satisfies this credential.
    Verify(r *http.Request) (ok bool, subject string)
    Scheme() string // for WWW-Authenticate
}
```
Effective `access.auth` compiles to a slice of `Verifier`; policy accepts if any
verifies. `WWW-Authenticate` on 401 reflects the configured schemes (e.g.
`Bearer`, `Basic realm="artifactsd"`).

## 2. Hashing & secret handling

| Secret | Algorithm | Storage |
|--------|-----------|---------|
| Object bearer token | SHA-256 | `token_hash` in metadata |
| API/management token | SHA-256 | `api_tokens.token_hash` |
| Share token (`share_id`) | none (it *is* a capability URL) | stored plaintext id, unguessable |
| Basic-auth password | argon2id | `password_hash` in metadata |
| Admin (OIDC) | n/a (token from issuer) | group-claim gated, env-configured |
| Admin (basic fallback) | argon2id | from env (`ARTIFACTSD_ADMIN_BASIC_PASSWORD[_HASH]`) |

- Argon2id parameters: tunable, default `time=2, memory=64MiB, threads=4`,
  16-byte salt, 32-byte key, encoded `$argon2id$v=19$m=...,t=...,p=...$salt$hash`.
- Cleartext secrets accepted only on write, hashed immediately, never persisted
  or logged, never returned on read (API returns presence booleans).
- Constant-time comparisons (`crypto/subtle`) for all hash checks.

## 3. Path validation (SPEC §17 — prevent traversal)

All collection-relative paths pass `cleanPath`:
- Reject if it contains `..`, NUL, control chars, or backslashes.
- Normalize to POSIX, strip leading `/`, collapse `//`, reject `.`/empty
  segments.
- Reject absolute paths and Windows drive/UNC forms.
- After joining to the data dir, verify the resolved absolute path is still
  within the collection's blob root (defense-in-depth, catches symlink/edge
  cases). With content-addressed blobs the blob path derives from a hash, not
  the user path, which removes most traversal surface; the logical path is still
  validated for the DB key and URL.
- Collection names validated against the slug regex ([02 §2](02-data-model.md)).

## 4. Existence non-disclosure (SPEC §17, decision C4)

- `private` and expired objects return **404** on public routes, never 403.
- For `protected`/auth-configured objects the challenge depends on the auth
  type (precedence oidc → basic → bearer):
  - **bearer only** → **404** to unauthenticated/invalid callers (capability;
    existence hidden).
  - **basic** → **401 `WWW-Authenticate: Basic`** (browser login dialog).
  - **oidc** → **302** to issuer for browsers, **401 Bearer** for API clients.
- Full matrix and rationale in [14 §3](14-access-control.md).

## 5. Rate limiting (SPEC §17)

- **Auth-failure limiter**: per-IP (and per-token-prefix) token-bucket on 401/403
  outcomes. After N failures in a window → `429` with `Retry-After`. Defaults
  configurable (`limits.auth_failures`, e.g. 10/min). In-memory with periodic
  GC; sufficient for single-node (SPEC §2 non-goal: clustering).
- **Upload size**: `limits.max_upload_size` enforced by a `http.MaxBytesReader`
  wrapper; streaming uploads are checked incrementally → `413`.
- **Per-collection file count**: `limits.max_file_count_per_collection`
  enforced on upload → `409`/`403`.

## 6. Transport security (SPEC §17, decision D4)

- Direct TLS via cert/key files **or** automatic TLS via **ACME/autocert
  (Let's Encrypt) from the first release** (`golang.org/x/crypto/acme/autocert`),
  selected by env/flags ([12](12-config.md)).
- Operable behind a reverse proxy without TLS (plain HTTP listener). Honor
  `X-Forwarded-For`/`X-Forwarded-Proto` only when `trust_proxy` is set, to avoid
  IP spoofing of the rate limiter and access logs.
- Secure cookies for the admin session (HttpOnly, Secure when TLS, SameSite).

## 7. Audit & logging (SPEC §17, decisions F5 / N6 / 12factor)

- **Access logs → stdout** (structured, one event per served request). They are
  treated as an event stream, not stored in the DB.
- **Application logs and errors → stderr** (slog).
- **Audit log** is **persisted in the `audit_logs` table** (SPEC §11/§17 audit
  trail + admin audit view) **and** mirrored to stderr: actor, action, target,
  request id, details. It records:
  - admin actions and all deletions (manual + janitor); and
  - **successful authenticated access to protected / auth-required objects**
    (decision N6): action `object.access`, `target` = collection/path (or share
    id), `actor` = the authenticated principal — OIDC `sub`/email, basic
    username, or, for bearer, the token prefix (the token itself is never
    logged). Public/unauthenticated reads are **not** audited (they go to the
    stdout access log only); this keeps the audit log a focused
    "who accessed restricted content" trail.
- No secrets, tokens, or password material ever logged.

## 8. Other hardening

- Reject unsafe response headers at write & serve time ([03 §6](03-metadata-policy.md)).
- Request body limits on all JSON endpoints.
- `Artifactsd-Metadata` header size-capped and schema-validated.
- Panics recovered by middleware → 500 without stack leakage to clients.
- Admin UI: CSRF tokens on state-changing forms; same-origin checks.
