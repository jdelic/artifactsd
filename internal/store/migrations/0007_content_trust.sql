-- Migration 0007: content trust (artifactsd-7tl.2).
-- Forward-only; destructive changes get a new numbered migration.
--
-- content.sandbox=false may only take effect for bytes an admin-scoped
-- principal wrote. nodes.content_trusted records that per file; serving forces
-- the CSP sandbox for untrusted content whatever the (possibly inherited or
-- share-level) metadata says. Existing files keep today's behaviour (trusted);
-- every write from now on sets the flag explicitly.
ALTER TABLE nodes ADD COLUMN content_trusted BOOLEAN NOT NULL DEFAULT 1;

-- Large uploads carry the trust of the principal that began the session (the
-- PUT itself is an anonymous capability). In-flight sessions are untrusted.
ALTER TABLE upload_sessions ADD COLUMN content_trusted BOOLEAN NOT NULL DEFAULT 0;
