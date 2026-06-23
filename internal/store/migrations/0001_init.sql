-- Migration 0001: initial schema (design/02 §3).
-- Forward-only; destructive changes get a new numbered migration.

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
    expires_at    TEXT,                        -- effective absolute expiry, nullable
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
    action      TEXT NOT NULL,                 -- e.g. file.delete, collection.create, expiry.delete, object.access
    target      TEXT,                          -- collection/path/share id
    details     TEXT,                          -- JSON
    request_id  TEXT
);
CREATE INDEX idx_audit_ts ON audit_logs(ts);
