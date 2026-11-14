-- Migration 0005: upload-link overwrite provenance (artifactsd-m9e.1, design/15 §3).
-- Forward-only; destructive changes get a new numbered migration.
--
-- An anonymous upload link may only replace files that it created itself, and
-- only when the link was created with allow_overwrite. nodes.upload_request_id
-- records which link wrote a file; any other writer (API PUT, large upload,
-- admin UI) clears it.

ALTER TABLE nodes ADD COLUMN upload_request_id INTEGER REFERENCES upload_requests(id) ON DELETE SET NULL;
ALTER TABLE upload_requests ADD COLUMN allow_overwrite BOOLEAN NOT NULL DEFAULT 0;
