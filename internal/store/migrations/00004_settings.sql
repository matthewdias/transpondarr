-- +goose Up
-- Runtime configuration overrides. Each row is one setting key (e.g. "qbit.url").
-- These take precedence over the TRANSPONDARR_* environment baseline, so the
-- Settings UI can edit integrations without an env change or restart. A section
-- is considered DB-managed once any of its keys is present (an empty value means
-- "explicitly cleared / disabled", distinct from "never set").
CREATE TABLE settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- +goose Down
DROP TABLE settings;
