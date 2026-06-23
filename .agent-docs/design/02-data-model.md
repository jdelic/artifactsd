# 02 — Data model

## 1. Object model (SPEC §4)

```
collection/            top-level published namespace, has a unique name
  folder/              path segment(s); may carry metadata; usually implicit
    file               a stored blob with metadata
```

Decisions:

- **Folders are implicit.** A file at `assets/img/chart.png` implies folders
  `assets/` and `assets/img/`. We do **not** require explicit folder creation.
  Folder metadata is optional and attached on demand (a folder row is
  materialized only when it gets metadata or a share). This keeps uploads simple
  while still supporting per-folder policy (SPEC §4, §8).
- Within a collection, a node is uniquely identified by its **normalized POSIX
  path** (no leading slash, no `.`/`..`, no empty segments). See
  [06](06-auth-security.md) for path validation.

## 2. Identifiers

| Entity | ID scheme | Example |
|--------|-----------|---------|
| Collection | caller-chosen `name`, slug-validated, unique | `sales-demo-2026` |
| Node (file/folder) | internal `int64` rowid + `(collection_id, path)` unique | — |
| Share | `share_id`: 12+ char base62 from CSPRNG | `7V9qWb3KxLm2` |
| Upload session | `upload_id`: `upl_` + base62 | `upl_8f31c2a9` |
| API token | `tok_` prefix + secret; only hash stored | `tok_...` |

Collection name rules: `^[a-z0-9][a-z0-9._-]{0,127}$`, no `..`, case-sensitive
stored but reject names colliding case-insensitively to avoid FS ambiguity.

Share IDs: ≥72 bits of entropy (12 base62 ≈ 71.5 bits; use 16 → 95 bits for
comfort). Generated with `crypto/rand`. Unguessable per SPEC §5.2.

## 3. SQLite schema (initial migration `0001_init`)

JSON columns hold the flexible metadata document ([03](03-metadata-policy.md));
denormalized columns exist where the janitor, sitemap, or listing must query
efficiently.

```sql
PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;

-- Collections -----------------------------------------------------------
CREATE TABLE collections (
    id           INTEGER PRIMARY KEY,
    name         TEXT NOT NULL UNIQUE,
    metadata     TEXT NOT NULL DEFAULT '{}',   -- JSON metadata document
    created_at   TEXT NOT NULL,                -- RFC3339 UTC
    updated_at   TEXT NOT NULL,
    -- denormalized from metadata for janitor/listing:
    visibility   TEXT NOT NULL DEFAULT 'private',
    expires_at   TEXT                          -- effective absolute expiry, nullable
);

-- Nodes (files and folders) --------------------------------------------
CREATE TABLE nodes (
    id            INTEGER PRIMARY KEY,
    collection_id INTEGER NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
    parent_id     INTEGER REFERENCES nodes(id) ON DELETE CASCADE,
    path          TEXT NOT NULL,               -- normalized, e.g. 'assets/chart.png'
    name          TEXT NOT NULL,               -- last segment
    kind          TEXT NOT NULL CHECK (kind IN ('file','folder')),
    -- file-only fields (NULL for folders):
    blob_id       TEXT,                        -- content-addressed blob ref
    size          INTEGER,
    content_type  TEXT,
    checksum      TEXT,                        -- sha256 hex of content
    metadata      TEXT NOT NULL DEFAULT '{}',  -- JSON metadata document (overrides)
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL,
    last_access_at TEXT,
    -- denormalized:
    visibility    TEXT,                        -- NULL = inherit
    expires_at    TEXT,                         -- effective absolute expiry, nullable
    UNIQUE (collection_id, path)
);
CREATE INDEX idx_nodes_collection ON nodes(collection_id);
CREATE INDEX idx_nodes_parent     ON nodes(parent_id);
CREATE INDEX idx_nodes_expiry     ON nodes(expires_at) WHERE expires_at IS NOT NULL;

-- Shares ----------------------------------------------------------------
CREATE TABLE shares (
    id            INTEGER PRIMARY KEY,
    share_id      TEXT NOT NULL UNIQUE,        -- the unguessable token in the URL
    collection_id INTEGER NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
    target_path   TEXT NOT NULL DEFAULT '',    -- '' = whole collection
    target_kind   TEXT NOT NULL,               -- collection|folder|file
    metadata      TEXT NOT NULL DEFAULT '{}',  -- share-level access/expiry/listing overrides
    created_at    TEXT NOT NULL,
    expires_at    TEXT,
    revoked_at    TEXT,
    last_access_at TEXT
);
CREATE INDEX idx_shares_expiry ON shares(expires_at) WHERE expires_at IS NOT NULL;

-- Upload sessions -------------------------------------------------------
CREATE TABLE upload_sessions (
    id            INTEGER PRIMARY KEY,
    upload_id     TEXT NOT NULL UNIQUE,
    collection_id INTEGER NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
    target_path   TEXT NOT NULL,
    metadata      TEXT NOT NULL DEFAULT '{}',
    content_type  TEXT,
    status        TEXT NOT NULL DEFAULT 'pending', -- pending|completed|aborted|expired
    temp_blob_id  TEXT,
    created_at    TEXT NOT NULL,
    expires_at    TEXT NOT NULL,
    completed_at  TEXT
);
CREATE INDEX idx_uploads_status ON upload_sessions(status, expires_at);

-- API tokens (management/admin) ----------------------------------------
CREATE TABLE api_tokens (
    id           INTEGER PRIMARY KEY,
    name         TEXT NOT NULL,
    token_hash   TEXT NOT NULL UNIQUE,         -- sha256 hex
    token_prefix TEXT NOT NULL,                -- first chars, for display/lookup hinting
    scopes       TEXT NOT NULL DEFAULT '[]',   -- JSON array, e.g. ["admin"] or ["collections:write"]
    created_at   TEXT NOT NULL,
    last_used_at TEXT,
    expires_at   TEXT,
    revoked_at   TEXT
);

-- Audit log (admin + deletion actions, SPEC §11/§17) -------------------
CREATE TABLE audit_logs (
    id          INTEGER PRIMARY KEY,
    ts          TEXT NOT NULL,
    actor       TEXT,                          -- token name / admin user / 'janitor'
    action      TEXT NOT NULL,                 -- e.g. file.delete, collection.create, expiry.delete
    target      TEXT,                          -- collection/path/share id
    details     TEXT,                          -- JSON
    request_id  TEXT
);
CREATE INDEX idx_audit_ts ON audit_logs(ts);

-- (No access_logs table: access logs are streamed to stdout, decision F5.)

-- Schema versioning ----------------------------------------------------
CREATE TABLE schema_migrations (
    version     INTEGER PRIMARY KEY,
    applied_at  TEXT NOT NULL
);
```

### Notes on denormalized columns
- `visibility` and `expires_at` are written whenever a node/collection's
  metadata changes, computed as the **effective** value for that object. They
  exist so the janitor (`WHERE expires_at <= now`), sitemap generation
  (`WHERE visibility='public' AND indexable`), and listings can run as simple
  indexed queries without re-resolving JSON per row.
- The authoritative source remains the `metadata` JSON; denormalized columns are
  a derived cache and are recomputed on write and on migration.

## 4. Migrations

- SQL files embedded via `embed.FS` under `internal/store/migrations/`, named
  `NNNN_description.sql`, applied in order, tracked in `schema_migrations`.
- `artifactsd migrate` runs them explicitly; `serve` runs pending migrations on
  startup (configurable `storage.auto_migrate`, default true).
- Forward-only for v1 (no down migrations); destructive changes get a new
  numbered migration.

## 5. Storage on disk (SPEC §18)

```
data/
  artifactsd.db            # SQLite (+ -wal, -shm)
  blobs/                  # content-addressed or path-mirrored blob store
  tmp/                    # in-progress uploads (temp files for atomic rename)
```

Blob layout is detailed in [07](07-storage-uploads.md). Default decision:
**content-addressed** blobs (`blobs/sha256/ab/cd/<hash>`), with the `nodes`
table mapping logical path → `blob_id`. This dedupes identical content and makes
atomic writes and integrity checks natural. (SPEC §18 shows a path-mirrored
layout as illustration; content-addressing is an implementation refinement that
preserves the same external behavior — confirmed by decision B1, with the data
dir portable to another server.)

## 6. Referential behavior

- Deleting a collection cascades to nodes, shares, and upload sessions.
- Deleting a folder node cascades to descendant nodes (via `parent_id`).
- Blob GC: a blob file is removed when no `nodes.blob_id` references it
  (reference count check during janitor sweep), since content-addressing means
  blobs may be shared.
