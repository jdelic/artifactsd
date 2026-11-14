-- Migration 0006: large-upload sessions remember whether metadata was supplied
-- (artifactsd-m9e.3). A session begun without metadata keeps the target file's
-- existing metadata when it replaces a file, instead of wiping it. Existing
-- sessions keep today's behaviour (their metadata replaces the target's).

ALTER TABLE upload_sessions ADD COLUMN metadata_supplied BOOLEAN NOT NULL DEFAULT 1;
