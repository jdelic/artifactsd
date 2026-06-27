# Security / Reliability Review - 2026-06-26

Reviewer: Codex.
Scope: v1.0.0 pre-release review of docs, closed beads history, and source code,
with emphasis on security boundaries, capability endpoints, expiry/non-disclosure,
and consistency between API/admin/public-serving paths.

Baseline branch at review start: `main`; review notes written on
`codex/artifactsd-c63/security-review-findings`.

## Summary

The core architecture is coherent and the prior M1 review findings appear to
have been addressed. The most important remaining release blocker is a browser
origin-boundary problem: active user artifacts are served from the same origin as
the cookie-authenticated admin UI. That makes normal admin CSRF defenses
insufficient when an admin opens an untrusted HTML artifact.

Six beads were filed from this pass:

| Ticket | Priority | Finding |
|--------|----------|---------|
| `artifactsd-c63` | P0 | Same-origin active artifacts can drive/read admin UI |
| `artifactsd-ku3` | P1 | Large-upload single-use capability is not atomic |
| `artifactsd-bhq` | P1 | Object OIDC auth can skip audience verification |
| `artifactsd-2bw` | P1 | Uploads can corrupt file/folder tree invariants |
| `artifactsd-jcs` | P2 | Directory listings leak expired child names |
| `artifactsd-8yy` | P2 | Configured default expiry is parsed but unused |

## Findings

### 1. Same-origin active artifacts can drive/read admin UI

Ticket: `artifactsd-c63` (bug, P0)

Artifacts are intentionally capable of being HTML reports, pages, and other
active browser content, and they are served from `/c/...` and `/s/...` on the
same origin as `/admin`. Admin sessions are cookie-based and CSRF-protected, but
same-origin artifact JavaScript bypasses the cross-origin assumptions behind
those controls:

- `/admin` CSRF checks accept same-origin requests and then validate a token
  (`internal/server/csrf.go:18`).
- Admin rendering embeds the CSRF token into same-origin HTML
  (`internal/admin/templates.go:73`).
- The token creation UI returns the one-time API token secret in the response
  (`internal/admin/tokens.go:48`).
- Public serving streams artifact-controlled HTML with metadata-controlled
  response headers, after only header filtering and content-type resolution
  (`internal/serve/serve.go:282`).

Exploit shape: an attacker gets an admin to open a malicious `/c/...` or `/s/...`
HTML artifact. That page can `fetch("/admin/tokens")`, read the CSRF token from
the returned admin HTML, submit the admin token form with `admin` scope, read the
one-time token secret from the response, and exfiltrate it. `HttpOnly`,
`SameSite`, CORS, and ordinary `/admin` response headers do not stop this
because the malicious code is same-origin.

Suggested single-domain fix: sandbox active artifact responses by default, e.g.
`Content-Security-Policy: sandbox allow-scripts allow-downloads` without
`allow-same-origin` for `text/html` and `image/svg+xml`, with an explicit
trusted-content opt-out if needed. Admin request-side checks such as Fetch
Metadata and admin-path Referer validation are still useful defense in depth,
but they should not be the primary boundary.

### 2. Large-upload single-use capability is not atomic

Ticket: `artifactsd-ku3` (bug, P1)

The large-upload handler documents the upload capability as single-use and
"consumed atomically on completion" (`internal/api/uploads.go:98`). The actual
sequence is:

1. Read the pending session (`internal/api/uploads.go:102`).
2. Store the blob (`internal/api/uploads.go:108`).
3. Upsert/replace the target node (`internal/api/uploads.go:140`).
4. Recompute metadata (`internal/api/uploads.go:144`).
5. Mark the session completed with a conditional `WHERE status = pending`
   (`internal/api/uploads.go:149`, `internal/store/uploads.go:78`).

Two concurrent `PUT`s to the same upload URL can both pass the initial pending
read. The loser will return 409 when `CompleteUploadSession` fails, but it may
already have replaced the target node with its blob. That violates the
capability's single-use guarantee and creates a race where a leaked URL can
mutate content after the legitimate uploader completed first.

Fix direction: atomically claim the pending session before committing the target
node, or perform session transition and node commit in one transaction whose
precondition includes `status = pending`. Add a concurrent double-`PUT` test
that asserts exactly one content body becomes visible.

### 3. Object OIDC auth can skip audience verification

Ticket: `artifactsd-bhq` (bug, P1)

The access-control design says object OIDC validation verifies the token
audience. The implementation only does that when metadata includes
`audience`; if it is empty, the verifier explicitly enables
`SkipClientIDCheck` (`internal/auth/oidc.go:94`). Metadata validation requires an
issuer and an authorization rule, but does not require an audience
(`internal/meta/validate.go:65`).

Impact: an object configured as `{"type":"oidc","issuer":"...","allowed_groups":
["staff"]}` can accept an ID token issued for some other client at the same
issuer, as long as the subject/group rule matches. `any_authenticated` without
audience is especially broad.

Fix direction: require `audience` on object OIDC entries, or add an explicit and
well-documented insecure opt-out. Add tests proving missing audience does not
silently disable audience verification.

### 4. Uploads can corrupt file/folder tree invariants

Ticket: `artifactsd-2bw` (bug, P1)

Both API and admin upload parent materialization accept any existing ancestor
node as a parent without checking that it is a folder:

- API helper: `internal/api/files.go:259`
- Admin helper: `internal/admin/uploads.go:182`

This permits inconsistent trees:

- If a file `a` exists, uploading `a/b.txt` sets `a` (a file) as the parent of
  `a/b.txt`.
- If a folder `a` with descendants exists, uploading a file at path `a` upserts
  the row to `kind=file` while retaining descendants underneath it.

The store schema allows this because `parent_id` references `nodes(id)` without
a kind constraint, and `UpsertNode` overwrites `kind` on path conflict.

Fix direction: reject path kind conflicts in the upload paths and folder
materialization paths. In particular, existing ancestors must be folders, and a
folder path should not be replaced by a file without an explicit delete/recreate
flow. Cover direct API uploads, large uploads, admin uploads, and folder metadata
materialization.

### 5. Directory listings leak expired child names

Ticket: `artifactsd-jcs` (bug, P2)

Direct public fetches of expired files return 404, and the crawl API suppresses
expired entries (`internal/api/crawl.go:202`). Directory listings, however, only
filter private and unlisted children (`internal/serve/listing.go:173`) and do
not evaluate each child's effective expiry.

Impact: a public browsable folder can reveal names and metadata for expired
children during the interval between expiry time and janitor deletion, even
though direct fetches are already non-disclosing.

Fix direction: carry parent effective expiry into `renderListing`, combine it
with each child `ExpiresAt`, and skip expired children. Include both regular and
share-rooted listing tests.

### 6. Configured default expiry is parsed but unused

Ticket: `artifactsd-8yy` (bug, P2)

Docs expose `ARTIFACTSD_DEFAULT_DELETE_AFTER_CREATION` as a default object
expiry. The config loader parses it into `Config.Policy.DeleteAfterCreation`
(`internal/config/load.go:137`), but the serving/API default metadata layers do
not include expiry (`internal/serve/serve.go:340`,
`internal/api/crawl.go:278`), and denormalization derives expiry only from the
object's stored metadata (`internal/store/denormalize.go:54`).

Impact: operators can set a default expiry, validation succeeds, and newly
created content still has no default expiry.

Fix direction: either implement this as a real default expiry layer used when
creating/denormalizing collections and nodes, or remove the advertised setting
before v1. If implemented, use the same duration grammar as metadata/docs and
add tests that new content receives the default expiry and the janitor sees it.

## Notes

- The previous M1 review file remains unchanged.
- No code fixes were made in this pass; the only repository change is this
  review note.
- No test suite was run because this was a documentation/ticketing review with
  no production code edits.
