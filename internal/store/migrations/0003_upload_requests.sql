-- Migration 0003: upload request links (design/15).
-- Forward-only; destructive changes get a new numbered migration.

-- Upload request links ---------------------------------------------------
CREATE TABLE upload_requests (
    id                     INTEGER PRIMARY KEY,
    request_id             TEXT NOT NULL UNIQUE,       -- ≥16 base62 chars (~95 bits), the capability token
    collection_id          INTEGER NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
    target_folder          TEXT NOT NULL DEFAULT '',   -- '' = root of collection
    label                  TEXT,                       -- shown on the upload form
    description            TEXT,                       -- longer text on the upload form
    file_metadata          TEXT NOT NULL DEFAULT '{}', -- metadata applied to each uploaded file (JSON)
    allow_uploader_access  INTEGER NOT NULL DEFAULT 0, -- 0/1 bool: show file URLs to uploader on success
    single_use             INTEGER NOT NULL DEFAULT 0, -- 0/1 bool: auto-close after first successful submission
    use_submission_folders INTEGER NOT NULL DEFAULT 0, -- 0/1 bool: nest files under {folder}/{submission_id}/
    max_files              INTEGER,                    -- optional cap on total files (across all submitters)
    max_file_size          INTEGER,                    -- optional per-file size override (≤ server max_upload_size)
    files_uploaded         INTEGER NOT NULL DEFAULT 0, -- running counter
    status                 TEXT NOT NULL DEFAULT 'active', -- active|closed|revoked|expired
    created_at             TEXT NOT NULL,
    expires_at             TEXT,                       -- NULL = no expiry
    revoked_at             TEXT,
    last_used_at           TEXT
);
CREATE INDEX idx_upload_requests_status ON upload_requests(status, expires_at);

-- Upload request submissions ---------------------------------------------
-- One row per POST to /r/{request_id} that successfully lands at least one file.
CREATE TABLE upload_request_submissions (
    id                INTEGER PRIMARY KEY,
    upload_request_id INTEGER NOT NULL REFERENCES upload_requests(id) ON DELETE CASCADE,
    submission_id     TEXT NOT NULL UNIQUE,   -- 8 base62 chars; folder name when use_submission_folders=true
    file_count        INTEGER NOT NULL DEFAULT 0,
    total_bytes       INTEGER NOT NULL DEFAULT 0,
    client_ip         TEXT,
    created_at        TEXT NOT NULL
);
CREATE INDEX idx_submissions_request ON upload_request_submissions(upload_request_id);
