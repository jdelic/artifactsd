-- Migration 0008: per-collection grants for API tokens (artifactsd-m9e.28,
-- design/16). Forward-only; destructive changes get a new numbered migration.
--
-- A restricted token reaches only the collections it holds a grant for, with
-- that grant's scopes. Existing tokens stay unrestricted. Grants reference
-- collections by id, so deleting a collection drops its grants and a new
-- collection reusing the name is not covered.
ALTER TABLE api_tokens ADD COLUMN restricted BOOLEAN NOT NULL DEFAULT 0;

CREATE TABLE token_grants (
    token_id      INTEGER NOT NULL REFERENCES api_tokens(id)  ON DELETE CASCADE,
    collection_id INTEGER NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
    scopes        TEXT    NOT NULL,
    created_at    TEXT    NOT NULL,
    PRIMARY KEY (token_id, collection_id)
);
CREATE INDEX idx_token_grants_collection ON token_grants(collection_id);
