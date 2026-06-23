# 08 — Expiry janitor

Implements SPEC §11 (expiry & deletion) plus upload-session and blob GC.

## 1. Responsibilities

A single background goroutine on a ticker (`janitor.interval`, default 60s):
1. Delete expired **nodes** (files/folders), **collections**, and **shares**.
2. Expire/abort stale **upload sessions** and remove their temp blobs.
3. GC **orphan blobs** (no referencing node) and stale temp files.
4. Write an **audit record** for every deletion (SPEC §11, §17).

A manual trigger (`POST /api/v1/maintenance/cleanup`, admin UI button, and
`artifactctl maintenance cleanup`) runs one pass on demand (SPEC §14).

## 2. Effective expiry source

Each node/collection/share carries a denormalized `expires_at`
([02 §3](02-data-model.md)) computed at write time from the resolved expiry rules
([03 §4](03-metadata-policy.md)):
`min(expires_at, created_at+delete_after_creation, last_access_at+delete_after_last_access)`
across the object and inherited levels (subject to `expiry.inherit`).

So the hot path is a single indexed query:
```sql
SELECT id, collection_id, path, blob_id FROM nodes
WHERE expires_at IS NOT NULL AND expires_at <= :now
ORDER BY expires_at LIMIT :batch;
```
(plus equivalents for `collections` and `shares`).

## 3. `delete_after_last_access` handling

This makes expiry a moving target. Approach:
- `last_access_at` is updated on serve, **throttled** (e.g. at most once per
  60s per node, coalesced in memory) to avoid write amplification on hot files.
- When `last_access_at` advances, the node's `expires_at` is recomputed and the
  denormalized column updated in the same throttled write.
- The janitor only trusts the denormalized `expires_at`; the throttle window is
  bounded so an object lives at most `window` longer than its strict deadline —
  acceptable for a cleanup system.

Flagged in OPEN-QUESTIONS: exact throttle window and whether access updates are
acceptable write load for the expected scale.

## 4. Deletion procedure (per object)

Within a transaction where practical:
1. Resolve descendants (for folder/collection) via `parent_id` / `collection_id`.
2. Collect candidate `blob_id`s.
3. Delete DB rows (cascade handles descendants).
4. For each candidate blob, GC if now unreferenced (`blob.Delete` with refcount
   check).
5. Insert `audit_logs` row(s): `action=expiry.delete`, actor=`janitor`, target,
   details (reason: `expires_at`/`delete_after_*`, size freed).

Ordering favors never leaving a *live* DB row pointing at a deleted blob:
DB rows are removed first; blob unlink may lag and is reconciled by GC. A blob
unlink failure is logged and retried next pass.

## 5. Blob & temp GC

- **Orphan blobs:** periodically (lower frequency, e.g. every N passes) scan for
  blob refs with zero referencing nodes and unlink. To bound cost, maintain
  deletions' candidate set from §4 rather than full scans; a full reconcile runs
  at startup and on a long interval.
- **Temp files:** remove `data/tmp/*` older than the upload TTL.
- **Upload sessions:** `status=pending AND expires_at <= now` → `expired`,
  temp blob removed.

## 6. Behavior guarantees (SPEC §11)

- Expired objects return **404** before the janitor even runs: the serve path
  checks effective expiry live, so deletion latency never exposes expired
  content. The janitor reclaims storage and writes audit trails.
- Deletion removes both blob data and metadata.
- Earliest effective expiry wins; `expiry.inherit:false` overrides
  ([03 §4](03-metadata-policy.md)).

## 7. Configuration

```yaml
janitor:
  interval: 60s
  batch_size: 500
  blob_gc_every: 30        # run blob orphan GC every N passes
  access_update_throttle: 60s
```

## 8. Observability

- Each pass logs counts (nodes/collections/shares/sessions deleted, bytes
  freed, errors) at info level.
- Failures (e.g. blob unlink errors) logged at warn and retried; persistent
  failures surface in `/readyz` degradation and admin stats.
