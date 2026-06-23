# artifactd — Open Questions (QA for Jonas)

This is the combined QA document requested during planning. Each item lists the
**context**, the **options**, and the **default I will proceed with** if you
don't override it (per AGENT_INSTRUCTIONS §7: pick the most reasonable
interpretation, record it, and proceed when unattended).

Please answer inline by editing the **Decision** line, or just tell me which
numbers to change. Items are grouped; the ones most worth your attention are
**flagged ⭐**.

---

## A. Product scope & deployment

**A1. Single-operator vs multi-user.** ⭐
The SPEC implies one operator and capability-style access (tokens, share URLs,
basic/OIDC), not per-user accounts with RBAC.
- Options: (a) single operator + API tokens with scopes [SPEC-aligned];
  (b) full multi-user accounts + roles.
- **Default: (a).** No user accounts in v1; admin auth + scoped API tokens only.

**A2. Repository / module name.**
Repo is `artifactsd` (trailing `s`); SPEC names the product/binary `artifactd`.
- **Default:** module `github.com/jdelic/artifactsd`, binaries `artifactd` /
  `artifactctl`. Confirm you want the binary spelled `artifactd` (no `s`).

**A3. Deployment artifacts.**
- Options: static binaries only; + Docker image; + systemd unit; + Helm.
- **Default:** static binaries (linux/darwin, amd64/arm64) via goreleaser **and**
  a minimal Docker image. systemd unit example in docs. No Helm in v1.

**A4. License.**
- **Default:** none assumed — please specify (MIT / Apache-2.0 / proprietary).
  Blocks adding `LICENSE` and headers. ⭐

---

## B. Storage & data

**B1. Blob layout: content-addressed vs path-mirrored.** ⭐
SPEC §18 illustrates `blobs/collections/<c>/<path>`. I propose content-addressed
(`blobs/sha256/...`) for dedupe + atomic writes + integrity, with the logical
path kept in SQLite. External behavior is identical.
- **Default:** content-addressed behind a `blob.Store` interface (swappable).
  Override if you specifically want on-disk files to mirror URLs (e.g. for
  out-of-band inspection or rsync of the tree).

**B2. SQLite driver (CGO-free requirement).**
- Options: `modernc.org/sqlite` (pure Go, no cgo) vs `mattn/go-sqlite3` (cgo,
  faster).
- **Default:** `modernc.org/sqlite` for static, cross-compilable binaries.
  Confirm CGO-free is a hard requirement (it shapes the build).

**B3. Database engine.**
SPEC says SQLite, single-node (clustering is a non-goal §2).
- **Default:** SQLite only for v1. Postgres is out of scope unless you want the
  store interface kept DB-agnostic now (small extra cost).

---

## C. Metadata & policy semantics

**C1. `access.auth` inheritance: replace vs append.** ⭐
When a folder/file sets `auth`, does it **replace** the inherited credential
list or **add** to it?
- **Default: replace** (a level that sets `auth` fully defines accepted
  credentials), to avoid silently widening access. Empty `[]` = no auth.

**C2. Multiple auth entries = any-of vs all-of.**
- **Default: any-of** (present a valid bearer OR basic OR oidc). all-of (require
  all) seems unintended.

**C3. Expiry inheritance override keyword.**
SPEC §11: "earliest effective expiry wins unless explicitly configured
otherwise." I'm implementing the override as `expiry.inherit: false` (stop
inheriting ancestor expiry).
- **Default:** `expiry.inherit: false`. Tell me if you prefer a different
  keyword/semantics (e.g. `expiry.override: replace`).

**C4. `protected` existence disclosure.**
`private`/expired → 404 (no existence leak). `protected` → 401/403 (existence
implied to challenged clients, standard HTTP). Full concealment uses `private` +
share URL.
- **Default:** as above. Flag if `protected` must also return 404 to
  unauthenticated callers.

**C5. Default visibility for new objects.**
SPEC §19 example defaults to `private`.
- **Default:** new collections/files are `private` unless made public
  explicitly (safe-by-default). Confirm — this means `artifactctl put` without
  flags is *not* publicly reachable.

---

## D. Auth & security

**D1. Management API authentication model.** ⭐
SPEC describes API tokens and admin auth but not the exact API auth scheme.
- **Default:** `Authorization: Bearer tok_...` API tokens with scopes
  (`admin`, `collections:read/write`, `shares:write`, ...). Admin UI uses a
  session cookie; admin can also mint API tokens. Confirm this is the intended
  management-auth model.

**D2. Large-upload PUT authorization.** ⭐
For `PUT /api/v1/uploads/{upload_id}`: is possession of the unguessable
`upload_id` sufficient, or must the PUT also present a management token?
- Options: (a) require token + id [more secure]; (b) id alone is a capability
  [simpler for handing the URL to a dumb client/agent].
- **Default: (a) require a write-scoped token AND the id.** SPEC §16's MCP flow
  has the *agent* PUT bytes, which argues for (b); I'll make it configurable
  (`uploads.url_is_capability`) and default to (a). Please pick the default.

**D3. Bearer token hashing.**
Object/API bearer tokens are high-entropy → stored as SHA-256 (fast, indexable).
Basic-auth + admin passwords → argon2id.
- **Default:** as above. (Using argon2 on bearer tokens would block O(1) token
  lookup for no real benefit given their entropy.)

**D4. TLS / ACME.**
- **Default:** direct cert/key files in v1 (SPEC §17); ACME/autocert (Let's
  Encrypt) deferred to M2. Confirm autocert isn't needed for the first usable
  release.

---

## E. UI & clients

**E1. Admin UI technology.** ⭐
- Options: (a) server-rendered `html/template` + htmx, no JS build step
  [single-binary friendly]; (b) a JS SPA (React/Svelte) with a build toolchain.
- **Default: (a).** The admin UI is CRUD; htmx avoids a Node build and large
  dependency surface. Override if you want a richer SPA.

**E2. CLI framework.**
- **Default:** `spf13/cobra` (the SPEC CLI has nested subcommands). Alternative:
  stdlib `flag` (lighter, more boilerplate). Confirm cobra is acceptable as a
  dependency.

**E3. MCP SDK.** ⭐
- Options: official `github.com/modelcontextprotocol/go-sdk` vs
  `mark3labs/mcp-go`.
- **Default:** official SDK if its API is stable enough at implementation time,
  else `mark3labs/mcp-go`. Any preference?

**E4. MCP transport.**
- **Default:** stdio for MVP (`artifactd mcp`); HTTP/SSE post-MVP.

---

## F. Behavior details (lower stakes)

**F1. `last_access_at` write throttling.**
`delete_after_last_access` needs access tracking. I'll throttle updates (≤once
/60s/node) so an object may outlive its strict deadline by up to the throttle
window. Acceptable?
- **Default:** 60s throttle window, configurable.

**F2. Resumable/chunked uploads.**
- **Default:** single-PUT streaming in v1; resumable uploads post-MVP. Confirm
  no need for multi-GB resumable uploads in the first release.

**F3. Content-type detection order.**
explicit metadata → stored → extension map → `http.DetectContentType` sniff.
- **Default:** as listed.

**F4. Default limits.**
`max_upload_size` 1 GiB, `max_file_count_per_collection` 10000, auth-failure
limit 10/min (SPEC §19 values).
- **Default:** as listed; tunable in config.

**F5. Access-log retention / volume.**
- **Default:** access logs on, 30d retention, stored in SQLite. Could become
  large; option to disable or sample. OK?

**F6. Folder rows materialization.**
Parent folder rows are always created on upload (cheap; simplifies tree/cascade)
even when they carry no metadata.
- **Default:** as above.

---

## G. Anything I'm missing?

If there are use cases, integrations, or constraints not in the SPEC (existing
infra, expected scale, auth provider for OIDC, branding for the admin UI), note
them here and I'll fold them into the design before implementation starts.
