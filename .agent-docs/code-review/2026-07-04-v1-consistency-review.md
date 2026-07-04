# v1.0 Pre-Release Consistency Review — 2026-07-04

Reviewer: Claude (Fable 5), final review pass before v1.0.
Scope: inconsistencies, duplications, and architectural issues across the CLI,
HTTP API, MCP adapter, admin UI, config, and documentation — with emphasis on
the **command/parameter surface**, since this is the last window in which
commands and flags can still change without a breaking release.

Baseline: `main` @ `26f5a24` (after PR #60). Prior reviews:
[M1 review](2026-06-25-m1-review.md) (architecture/duplication) and the
[security review](2026-06-26-security-review.md) (all six findings resolved).
This document follows the cumulative convention: append, don't overwrite.

Findings are filed as beads tickets under epic **`artifactsd-b6l`**.

## TL;DR

Build, `go vet`, `golangci-lint`, and the full test suite are green. The
architecture remains sound: `pkg/apiclient` is still the only HTTP client, the
policy decision path is still centralized, and the two duplications flagged in
M1 were resolved (`resolveShareTarget` now delegates to
`store.ResolveShareTarget` in both packages). Config env vars match design/12
exactly (doc'd set ≡ implemented set, verified by diff).

The headline problem is not the code — it's that **the published command
surface and the implemented command surface have drifted apart**. The
user-facing CLI reference, the quickstart, the agent-facing SKILL.md, and all
four SPEC §20 use-case recipes document flags that do not exist; every one of
those recipes exits with `unknown flag` (verified by running them). Since
SKILL.md is what `artifactctl skill install` feeds to AI agents, agents are
actively taught commands that fail. That, plus a handful of smaller
API/CLI/MCP asymmetries, is what should be settled before v1.0.

## Baseline (as reviewed)

| Gate | Result |
|------|--------|
| `go build ./...` | clean |
| `go vet ./...` | clean |
| `golangci-lint run` | clean |
| `go test ./...` | all packages pass |

Code size: ~18.2k LOC non-test Go.

## Findings

### 1. CLI is missing its documented visibility/auth/expiry/header flags — `artifactsd-4k4` (bug, **P1, release blocker**)

`docs/cli.md`, `docs/quickstart.md`, `SKILL.md`, design/10 §2, and SPEC §20
all specify:

- `collection create [--public|--unlisted|--protected|--private] [--expires <ts>] [--expires-in <dur>]`
- `put [--header k=v] [--public|--protected|--bearer-token <t>|--basic <u:p>] [--expires/--expires-in] [--no-index]`
- `put-tree [--public] [--no-index] [--header k=v]`

Implemented: `collection create` has only `--visibility` and **no expiry
flags**; `put` has only `--content-type`; `put-tree` has none of the three.
Verified: `artifactctl collection create foo --private` → `unknown flag:
--private`. All four SPEC §20 recipes fail as published.

**Recommendation: implement the flags** (they are the SPEC contract and
design-10 surface), rather than rewriting four documents around the gap. The
share-create metadata builder (`cmd/artifactctl/share.go`, incl. the
`--bearer-token`-with-no-value auto-generation) is directly reusable for `put`.

### 2. `--expires`/`--expires-in` coverage is inconsistent — `artifactsd-599` (task, P2)

share create: both · upload-request create: both · meta set: only `--expires` ·
token create: only `--expires-in` · collection create/put/put-tree: neither.
All feed the same `expiresAtFrom()` helper; pure flag wiring. Make the pair
uniform everywhere expiry is accepted.

### 3. `?depth=` means opposite things on `/tree` vs `/crawl` — `artifactsd-y6s` (bug, P2)

- `GET .../tree?depth=`: empty/0 = **unlimited**; negative/invalid silently
  becomes **100** (`parseDepth` → `parseLimit` clamp).
- `GET .../crawl?depth=`: empty/0 = **1 level**; negative = **unlimited**.

Same parameter, sibling endpoints, contradictory conventions — and it leaks
into the CLI help texts (`tree --depth 0` = everything, `crawl --depth 0` = one
level) and the MCP `artifact_list_tree` schema, which documents tree depth 0 as
"server default (immediate children)" — wrong (it's unlimited). This is a wire
contract; settle it before v1.0 freezes it.

### 4. `DELETE /files/{path}` cascade-deletes folders without a kind guard — `artifactsd-a0c` (bug, P2)

`deleteFile` never checks `node.Kind`; with `parent_id ... ON DELETE CASCADE`,
deleting a folder path through the **files** route removes the whole subtree.
The folder routes are guarded in the opposite direction (409 "path is a file,
not a folder"). Net effect: `artifactctl rm coll/folder` — a command named for
single-file removal, with no `-r` and no confirmation — silently performs a
recursive delete. Add the symmetric 409 guard; optionally give the CLI a
deliberate folder-delete path afterward.

### 5. Collection & folder metadata unreachable from CLI/MCP — `artifactsd-2db` (task, P2)

`PATCH /collections/{c}/meta` and the folder-materializing
`PATCH .../folders/{path}/meta` exist (and `apiclient.PatchCollectionMeta` is
implemented) but nothing exposes them: `meta set` requires a
`<collection>/<path>` target (collection-level unreachable) and routes through
the **files** endpoint (404 on unmaterialized folders). MCP's metadata tools
say "file or folder" but share the limitation. After `collection create`
there is no CLI way to change a collection's policy or expiry at all.
Recommend: `meta get/set` accepts a bare `<collection>` target and routes
folder targets through the folders endpoints; mirror in MCP.

### 6. upload-request `file_metadata` settable by nobody; admin form minimal — `artifactsd-nml` (task, P3)

The API accepts `file_metadata` (validated, secrets hashed) per design/15, but
the CLI lacks the design-15 §6 flags (`--public|--unlisted|--private`,
`--no-index`, `--crawlable`), the MCP tool has no such input, and the admin UI
hardcodes `{}` — its create form also omits description / max_files /
max_file_size / allow_uploader_access / use_submission_folders, all of which
API+CLI+MCP support. The admin form's single `expires_in` field accepting
either RFC3339 or a duration diverges from the CLI's split-flag convention.

### 7. `put` never uses large-upload sessions — `artifactsd-5zb` (task, P3)

docs/cli.md and design/10 §4 map `put` to "PUT ... (or begin-upload for large
files)", but only `put-tree` has the 8 MiB threshold cutover. Give `put` the
same `--large-upload-threshold` behavior via a shared helper.

### 8. Reference-doc drift — `artifactsd-6v9` (chore, P2)

- `docs/cli.md`: nonexistent flags (finding 1); **no `upload-request` section**
  despite six subcommands; mapping table missing the upload-request rows.
- `docs/http-api.md`: missing the entire `/api/v1/upload-requests` section and
  the public `/r/{request_id}` routes (zero mentions).
- `docs/mcp.md`: missing the four `artifact_*_upload_request` tools.
- design/15 vs impl: `--target-folder` (impl) vs `--folder` (design);
  `get` (impl) vs `show` (design); `submissions` subcommand undocumented;
  design says revoke aliases close, impl correctly distinguishes close
  (PATCH `status=closed`) from revoke (DELETE → `revoked`) — update design to
  the better implementation.

Do after finding 1 so docs are written against the final surface.

### 9. Verb/flag symmetry polish — `artifactsd-9mp` (chore, P4)

`collection show` vs `upload-request get` (pick `show`, keep alias);
`--crawlable` has no `--no-crawlable` anywhere (once set, un-settable from the
CLI); `meta set` lacks `--list/--no-list`. Deletion verbs
(delete/rm/revoke/close) were reviewed and are **deliberately consistent**
(delete = data, revoke = capability) — no change, recorded to avoid
relitigation.

### 10. Cleanups — `artifactsd-qa8` (chore, P4)

- `ensureParents` duplicated verbatim (~40 lines) in `internal/api/files.go`
  and `internal/admin/uploads.go` (acknowledged in a comment). The invariant is
  already centralized in `store.UpsertNode`; move the materialization walk into
  the store and dedupe `lastSegment`/`strPtrOrNil`.
- `parseByteSize` (CLI upload-request) uses 1024-based multipliers under SI
  names (KB/MB/GB/TB).
- design/04 §1 declares `?limit=&cursor=` for all list endpoints; only
  `GET /collections` implements it. At the stated scale, recommend amending
  design/04 rather than implementing.

## Explicitly checked, no issues

- **Anti-duplication invariants from M1 still hold**: `pkg/apiclient` is the
  only HTTP client used by CLI + MCP; policy decisions stay in
  `internal/policy`; `resolveShareTarget` (M1 finding 2) is now shared via
  `store.ResolveShareTarget`.
- **Config surface**: documented env vars in `docs/configuration.md` diff-equal
  to the `ARTIFACTSD_*` set read in `internal/config` (43 vars). Daemon
  subcommands match design/12 §6 exactly.
- **Security review fixes** (sandbox CSP, upload-session claim ordering, OIDC
  audience, kind-conflict guard, expired-listing filter, default expiry) are
  present on `main`; no regressions spotted in the touched areas.
- **ID/prefix conventions** (`tok_`, `upl_`, `urq_`, bare share IDs per SPEC)
  are coherent; scopes in `token create` help match `auth.KnownScopes`.
- **CLI exit codes** implement design/10 §3 (0/1/2/4/5) via typed error
  mapping.
- **MCP tool set** matches design/11 + design/15 §8 (10 + 4 tools); error
  mapping and URL absolutization as designed.
- **Admin/API error-envelope difference** (`{"error":"msg"}` htmx JSON vs the
  API's coded envelope) is intentional per consumer; left as-is.

## Suggested order

1. `artifactsd-4k4` — the flag surface (blocker; everything in docs waits on it).
2. `artifactsd-y6s` + `artifactsd-a0c` — wire-contract fixes (cheap, breaking-if-later).
3. `artifactsd-599`, `artifactsd-2db` — CLI capability/uniformity.
4. `artifactsd-6v9` — docs rewrite against the settled surface.
5. `artifactsd-nml`, `artifactsd-5zb`, then P4s as time allows; none block v1.0.

## Resolution (2026-07-04)

All ten findings were fixed on this same branch
(`claude/artifactsd-b6l/v1-consistency-review`, Claude agent), one cohesive
commit per finding; `go build`, `go vet`, `golangci-lint`, and the full test
suite are green after every commit. Operator decisions taken during the work:

- **Flag grammar** (`artifactsd-4k4`): implemented the documented surface.
  Additionally, `--bearer-token` became a plain value flag and auto-generation
  moved to an explicit `--generate-bearer-token` — pflag cannot parse both a
  bare optional-value flag and a space-separated value, and the operator chose
  grammar consistency over the SPEC §20.2 recipe (recipe updated). The CLI now
  deliberately has **no optional-value flags**.
- **Depth** (`artifactsd-y6s`): omitted/0 = full tree, positive = N levels,
  else 400 — on both `/tree` and `/crawl`.
- **Folder deletes** (`artifactsd-a0c`): files route 409s on folders;
  `rm -r` added, mapping to the folders DELETE.
- **Metadata targets** (`artifactsd-2db`): `meta get/set` accepts a bare
  collection (collection meta), trailing slash (folder, materialized on set),
  or file path; MCP metadata tools follow the same convention; new
  `GET /collections/{c}/meta`.
- **Byte sizes** (`artifactsd-qa8`/`nml`): all units 1024-based (KB ≡ KiB),
  IEC spellings accepted — shared `internal/units.ParseByteSize` used by CLI
  and admin.
- **Expiry pair** (`artifactsd-599`): every expiry-taking command (and the
  admin upload-request form) takes the `--expires`/`--expires-in` pair.
- **upload-request parity** (`artifactsd-nml`): file_metadata settable from
  CLI/MCP/admin; admin form carries the full field set; `revoke` now
  escalates a closed link (store fix).
- **`put` threshold** (`artifactsd-5zb`), **polish** (`artifactsd-9mp`:
  `upload-request show`, `--no-crawlable`, `meta set --list/--no-list`),
  **dedup** (`artifactsd-qa8`: `store.EnsureParentFolders`), and the **docs
  rewrite** (`artifactsd-6v9`) landed as specified in the tickets.

Beyond the review findings, the operator requested and received metadata
provenance tooling: `meta.ResolveTraced` (single merge implementation with an
optional tracer), `chain` + `sources` on all meta GET endpoints, and
`artifactctl meta explain [--full-chain]`.
