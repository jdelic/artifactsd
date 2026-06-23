# 07 — Storage & uploads

## 1. Blob store (SPEC §18)

The `blob` package abstracts byte storage behind an interface so the FS backend
can later be swapped (S3 is an explicit non-goal for v1, SPEC §2):

```go
type Store interface {
    Put(ctx context.Context, r io.Reader) (ref BlobRef, size int64, sum string, err error)
    Open(ctx context.Context, ref BlobRef) (io.ReadSeekCloser, error)
    Delete(ctx context.Context, ref BlobRef) error
    Exists(ctx context.Context, ref BlobRef) (bool, error)
}
```

### Layout (content-addressed)
```
data/blobs/sha256/ab/cd/abcdef...   # file content keyed by sha256
data/tmp/<random>                    # in-progress writes
```
- `Put` streams to `data/tmp/<rand>`, hashing as it writes, fsyncs, then renames
  to the final sharded path (atomic on same filesystem). Returns the
  `sha256:<hex>` ref.
- Identical content dedupes naturally (same hash → same file). The `nodes` row
  maps logical path → `blob_id`.
- `Delete` is reference-counted: only unlink when no `nodes.blob_id` references
  the hash (checked under the janitor; see [08](08-expiry-janitor.md)).

> Note: SPEC §18 illustrates a path-mirrored layout
> (`blobs/collections/<c>/<path>`). Content-addressing yields identical external
> behavior with dedupe + atomicity benefits. This is flagged in
> OPEN-QUESTIONS for confirmation; the `Store` interface makes the choice
> swappable.

### Integrity
- `checksum` (sha256) stored per node; used for `ETag` and verifiable re-reads.
- Optional periodic scrub job (post-MVP) re-hashes blobs to detect bitrot.

## 2. Direct (small) uploads

`PUT /api/v1/collections/{c}/files/{path...}` ([04 §3](04-http-api.md)):
1. Auth + scope check; path validation ([06 §3](06-auth-security.md)).
2. Wrap body in `MaxBytesReader(max_upload_size)`.
3. `blob.Put` streams → temp → hash → final.
4. In one DB transaction: upsert `nodes` row (size, content_type, checksum,
   blob_id, metadata), materialize parent folder rows as needed, recompute
   denormalized `visibility`/`expires_at`.
5. On any failure after `blob.Put`, the freshly written blob is left for the GC
   sweep (safe because unreferenced) — or deleted immediately if not shared.
6. Audit entry; return summary JSON.

`Artifactd-Metadata` header (base64url JSON) lets binary uploads carry a metadata
document without a multipart body (SPEC §12.2).

## 3. Large uploads (SPEC §12.3)

Two-step to avoid buffering huge bodies through the JSON API:

1. `POST /api/v1/collections/{c}/uploads` creates an `upload_sessions` row with
   target path, metadata, content-type, an `upload_id`, and `expires_at`
   (default short TTL, e.g. 1h). Returns `upload_url`.
2. `PUT /api/v1/uploads/{upload_id}` streams bytes:
   - Authorized by possession of the `upload_id` (capability) **plus** the
     management token used to create it (the session records the creating token;
     the PUT must present a token with write scope, or the unguessable id alone
     if `uploads.url_is_capability` — default require token). Flagged in
     OPEN-QUESTIONS.
   - Streams to a temp blob with size limit + hashing.
   - On success: commit the node (same transaction logic as §2), mark session
     `completed`.
3. `GET /api/v1/uploads/{id}` → status; `DELETE` aborts and discards temp blob.

Abandoned/expired sessions and their temp blobs are reaped by the janitor.

Resumable/multipart chunked uploads are **out of scope for v1** (single PUT
stream). Noted as a possible post-MVP enhancement.

## 4. Folder & tree operations

- Folders are materialized rows only when metadata/shares attach, or as lazy
  parents of files (for `parent_id` linkage and tree queries). Implementation
  may always create parent folder rows on upload for simpler tree queries — a
  cheap row per directory. Decision: **always create parent folder rows** on
  upload (simplifies listing/cascade); cost is negligible.
- `DELETE` folder cascades via `parent_id` (`ON DELETE CASCADE`) and triggers
  blob GC for descendants.

## 5. Quotas & limits

- `limits.max_upload_size` (default 1 GiB, SPEC §19).
- `limits.max_file_count_per_collection` (default 10000, SPEC §19) checked on
  upload.
- Optional total-bytes quota per collection (post-MVP).

## 6. Crash safety

- WAL-mode SQLite + atomic blob rename means a crash mid-upload leaves at worst
  an orphan temp/blob file (GC'd) and no partial DB state (transaction rolls
  back).
- On startup the janitor reconciles: temp files older than TTL removed; blobs
  with no referencing node GC'd; upload sessions past TTL marked `expired`.
