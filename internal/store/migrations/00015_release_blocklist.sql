-- +goose Up
-- Per-series failure memory for a specific release (issue #118). A grab row is
-- per wanted item and is overwritten by the next attempt, so nothing outlived a
-- failure and the sweep re-derived the same ranking forever. Expired entries are
-- filtered, never deleted: the row carries failures, so deleting on expiry would
-- reset the escalation ladder and no release could ever become permanent.
CREATE TABLE release_blocklist (
    id               INTEGER PRIMARY KEY,
    series_id        INTEGER NOT NULL REFERENCES series(id) ON DELETE CASCADE,
    info_hash        TEXT NOT NULL,
    release_title    TEXT NOT NULL,
    normalized_title TEXT NOT NULL,
    reason           TEXT NOT NULL,
    failures         INTEGER NOT NULL DEFAULT 1,
    blocked_until    TEXT,
    created_at       TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at       TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Torznab often omits the infohash, so the normalized title is the identity that
-- always exists. Hash matching runs in Go over the series' loaded rows, so it
-- needs no index of its own.
CREATE UNIQUE INDEX idx_release_blocklist_title ON release_blocklist(series_id, normalized_title);

-- +goose Down
DROP INDEX idx_release_blocklist_title;
DROP TABLE release_blocklist;
