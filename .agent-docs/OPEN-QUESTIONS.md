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
- **Decision:** OpenID Connect support should go in now and the access
  configuration should allow for whitelisting a `group` claim or a user id
  (email), or any authenticated user. This is separate and additionally to
  htaccess or bearer token auth. Admin access should be completely OIDC-gated
  and depend on an envvar-configurable group claim being present.

**A2. Repository / module name.**
Repo is `artifactsd` (trailing `s`); SPEC names the product/binary `artifactd`.
- **Default:** module `github.com/jdelic/artifactsd`, binaries `artifactd` /
  `artifactctl`. Confirm you want the binary spelled `artifactd` (no `s`).
- **Decision:**: artifactsd is the canonical name. Update all non-matching
  references.

**A3. Deployment artifacts.**
- Options: static binaries only; + Docker image; + systemd unit; + Helm.
- **Default:** static binaries (linux/darwin, amd64/arm64) via goreleaser **and**
  a minimal Docker image. systemd unit example in docs. No Helm in v1.
- **Decision:** As described (no helm, minimal docker, systemd docs).

**A4. License.**
- **Default:** none assumed — please specify (MIT / Apache-2.0 / proprietary).
  Blocks adding `LICENSE` and headers. ⭐
- **Decision:** BSD 3-clause, identifying me (Jonas Maurus) as copyright holder.

---

## B. Storage & data

**B1. Blob layout: content-addressed vs path-mirrored.** ⭐
SPEC §18 illustrates `blobs/collections/<c>/<path>`. I propose content-addressed
(`blobs/sha256/...`) for dedupe + atomic writes + integrity, with the logical
path kept in SQLite. External behavior is identical.
- **Default:** content-addressed behind a `blob.Store` interface (swappable).
  Override if you specifically want on-disk files to mirror URLs (e.g. for
  out-of-band inspection or rsync of the tree).
- **Decision:** I want a copy of a filetree to be valid on another server, but
  filetrees do not have to match "server trees/urls".

**B2. SQLite driver (CGO-free requirement).**
- Options: `modernc.org/sqlite` (pure Go, no cgo) vs `mattn/go-sqlite3` (cgo,
  faster).
- **Default:** `modernc.org/sqlite` for static, cross-compilable binaries.
  Confirm CGO-free is a hard requirement (it shapes the build).
- **Decision:** CGO is fine.

**B3. Database engine.**
SPEC says SQLite, single-node (clustering is a non-goal §2).
- **Default:** SQLite only for v1. Postgres is out of scope unless you want the
  store interface kept DB-agnostic now (small extra cost).
- **Decision:** This is meant to be easy to deploy, and above all else
  self-contained. So go ahead with sqlite only.

---

## C. Metadata & policy semantics

**C1. `access.auth` inheritance: replace vs append.** ⭐
When a folder/file sets `auth`, does it **replace** the inherited credential
list or **add** to it?
- **Default: replace** (a level that sets `auth` fully defines accepted
  credentials), to avoid silently widening access. Empty `[]` = no auth.
- **Decision:** replace.

**C2. Multiple auth entries = any-of vs all-of.**
- **Default: any-of** (present a valid bearer OR basic OR oidc). all-of (require
  all) seems unintended.
- **Decision:** any-of confirmed.

**C3. Expiry inheritance override keyword.**
SPEC §11: "earliest effective expiry wins unless explicitly configured
otherwise." I'm implementing the override as `expiry.inherit: false` (stop
inheriting ancestor expiry).
- **Default:** `expiry.inherit: false`. Tell me if you prefer a different
  keyword/semantics (e.g. `expiry.override: replace`).
- **Decision:** Earliest expiry in a tree wins always. Making that
  configurable leads to dumb edge cases like "folder expires at 1, file in
  folder expires at 2 but disables inheritance, now folder must stick around
  until 2". Let's avoid that issue altogether. In the above scenario, file
  gets deleted at 1 when when its containing folder gets deleted.

**C4. `protected` existence disclosure.**
`private`/expired → 404 (no existence leak). `protected` → 401/403 (existence
implied to challenged clients, standard HTTP). Full concealment uses `private` +
share URL.
- **Default:** as above. Flag if `protected` must also return 404 to
  unauthenticated callers.
- **Decision:** protected should return 404 to unauthenticated callers for
  Bearer-token-auth. htaccess auth requires 401/403 to display the login dialog
  and OIDC redirects to the OpenID Connect server.

**C5. Default visibility for new objects.**
SPEC §19 example defaults to `private`.
- **Default:** new collections/files are `private` unless made public
  explicitly (safe-by-default). Confirm — this means `artifactctl put` without
  flags is *not* publicly reachable.
- **Decision:** I don't understand this. Files/folders/collections do not
  require auth unless configured to do so and are therefor publicly reachable
  at their URL, which might be hard to guess.

---

## D. Auth & security

**D1. Management API authentication model.** ⭐
SPEC describes API tokens and admin auth but not the exact API auth scheme.
- **Default:** `Authorization: Bearer tok_...` API tokens with scopes
  (`admin`, `collections:read/write`, `shares:write`, ...). Admin UI uses a
  session cookie; admin can also mint API tokens. Confirm this is the intended
  management-auth model.
- **Decision:** Admin UI uses OIDC and session cookie or htaccess auth. 
  Admin can mint Bearer tokens. The admin account is configured via envvars.

**D2. Large-upload PUT authorization.** ⭐
For `PUT /api/v1/uploads/{upload_id}`: is possession of the unguessable
`upload_id` sufficient, or must the PUT also present a management token?
- Options: (a) require token + id [more secure]; (b) id alone is a capability
  [simpler for handing the URL to a dumb client/agent].
- **Default: (a) require a write-scoped token AND the id.** SPEC §16's MCP flow
  has the *agent* PUT bytes, which argues for (b); I'll make it configurable
  (`uploads.url_is_capability`) and default to (a). Please pick the default.
- **Decision:** Agents need to be able to upload. I'd make the upload URLs
  hard to guess and have reasonable expiry rules and not require additional
  auth.

**D3. Bearer token hashing.**
Object/API bearer tokens are high-entropy → stored as SHA-256 (fast, indexable).
Basic-auth + admin passwords → argon2id.
- **Default:** as above. (Using argon2 on bearer tokens would block O(1) token
  lookup for no real benefit given their entropy.)
- **Decision:** agreed.

**D4. TLS / ACME.**
- **Default:** direct cert/key files in v1 (SPEC §17); ACME/autocert (Let's
  Encrypt) deferred to M2. Confirm autocert isn't needed for the first usable
  release.
- **Decision:** ACME support would be nice from the start.

---

## E. UI & clients

**E1. Admin UI technology.** ⭐
- Options: (a) server-rendered `html/template` + htmx, no JS build step
  [single-binary friendly]; (b) a JS SPA (React/Svelte) with a build toolchain.
- **Default: (a).** The admin UI is CRUD; htmx avoids a Node build and large
  dependency surface. Override if you want a richer SPA.
- **Decision:** Simple beats complex here.

**E2. CLI framework.**
- **Default:** `spf13/cobra` (the SPEC CLI has nested subcommands). Alternative:
  stdlib `flag` (lighter, more boilerplate). Confirm cobra is acceptable as a
  dependency.
- **Decision:** The CLI must be awesome to use. Cobra and any other dependency
  that serves that goal is fine, like stuff from https://github.com/charmbracelet.

**E3. MCP SDK.** ⭐
- Options: official `github.com/modelcontextprotocol/go-sdk` vs
  `mark3labs/mcp-go`.
- **Default:** official SDK if its API is stable enough at implementation time,
  else `mark3labs/mcp-go`. Any preference?
- **Decision:** No preference.

**E4. MCP transport.**
- **Default:** stdio for MVP (`artifactd mcp`); HTTP/SSE post-MVP.
- **Decision:** Do not do one then switch to the other. Stick with the intended
  system.

---

## F. Behavior details (lower stakes)

**F1. `last_access_at` write throttling.**
`delete_after_last_access` needs access tracking. I'll throttle updates (≤once
/60s/node) so an object may outlive its strict deadline by up to the throttle
window. Acceptable?
- **Default:** 60s throttle window, configurable.
- **Decision:** yes.

**F2. Resumable/chunked uploads.**
- **Default:** single-PUT streaming in v1; resumable uploads post-MVP. Confirm
  no need for multi-GB resumable uploads in the first release.
- **Decision:** resumable is nice and should go onto a milestone.

**F3. Content-type detection order.**
explicit metadata → stored → extension map → `http.DetectContentType` sniff.
- **Default:** as listed.

**F4. Default limits.**
`max_upload_size` 1 GiB, `max_file_count_per_collection` 10000, auth-failure
limit 10/min (SPEC §19 values).
- **Default:** as listed; tunable in config.
- **Decision:** Config should be command-line parameters and envvars (12factor).

**F5. Access-log retention / volume.**
- **Default:** access logs on, 30d retention, stored in SQLite. Could become
  large; option to disable or sample. OK?
- **Decision:** Access logs go on stdout, errors and logs go on stderr.

**F6. Folder rows materialization.**
Parent folder rows are always created on upload (cheap; simplifies tree/cascade)
even when they carry no metadata.
- **Default:** as above.

---

## G. Anything I'm missing?

If there are use cases, integrations, or constraints not in the SPEC (existing
infra, expected scale, auth provider for OIDC, branding for the admin UI), note
them here and I'll fold them into the design before implementation starts.

Decision:
- Follow 12factor
- Put some real thought into the user access options and configuration.

