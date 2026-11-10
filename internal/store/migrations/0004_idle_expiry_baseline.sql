-- Migration 0004: idle-expiry baseline (artifactsd-6b4, design/03 §4, design/08 §3).
-- Forward-only; destructive changes get a new numbered migration.
--
-- delete_after_last_access used to fall back to created_at whenever an object
-- had no recorded access. Collections had no last_access_at at all and shares
-- never wrote theirs, so an idle rule on either was really a creation-relative
-- rule that never moved — setting "30d" on an older collection produced an
-- expires_at in the past and the janitor deleted it.

-- Collections now track access like nodes and shares.
ALTER TABLE collections ADD COLUMN last_access_at TEXT;

-- No access history exists for collections, so start every idle clock now
-- rather than at creation.
UPDATE collections SET last_access_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now');

-- Likewise for never-accessed nodes and shares that carry an idle rule: their
-- clocks were effectively anchored at creation; re-anchor them to now.
UPDATE nodes SET last_access_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
 WHERE last_access_at IS NULL
   AND json_extract(metadata, '$.expiry.delete_after_last_access') IS NOT NULL;
UPDATE shares SET last_access_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
 WHERE last_access_at IS NULL
   AND json_extract(metadata, '$.expiry.delete_after_last_access') IS NOT NULL;

-- Deferred maintenance the server runs once at startup, before the janitor
-- starts. The denormalized expires_at depends on duration parsing that lives in
-- Go, so it can't be recomputed here; this flag asks for a full recompute.
CREATE TABLE maintenance_flags (
    name       TEXT PRIMARY KEY,
    created_at TEXT NOT NULL
);
INSERT INTO maintenance_flags (name, created_at)
VALUES ('recompute_denormalized', strftime('%Y-%m-%dT%H:%M:%SZ', 'now'));
