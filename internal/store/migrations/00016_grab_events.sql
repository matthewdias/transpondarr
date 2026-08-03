-- +goose Up
CREATE TABLE grab_events (
    id             INTEGER PRIMARY KEY,
    -- wanted_item_id is a plain column, no FK: a historical ref must not fight
    -- the series cascade.
    series_id      INTEGER NOT NULL REFERENCES series (id) ON DELETE CASCADE,
    wanted_item_id INTEGER NOT NULL,
    item_number    INTEGER NOT NULL DEFAULT 0,
    item_kind      TEXT    NOT NULL DEFAULT 'episode',
    info_hash      TEXT    NOT NULL,
    release_title  TEXT    NOT NULL DEFAULT '',
    event          TEXT    NOT NULL,
    detail         TEXT    NOT NULL DEFAULT '',
    created_at     TEXT    NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX idx_grab_events_created ON grab_events (created_at);
CREATE INDEX idx_grab_events_series ON grab_events (series_id, created_at);

-- Backfill: each surviving grab row's current status stands in for its latest
-- event; its created_at is the grab time, since settle times were never stored.
INSERT INTO grab_events (series_id, wanted_item_id, item_number, item_kind, info_hash, release_title, event, created_at)
SELECT w.series_id, g.wanted_item_id, COALESCE(w.number, 0), w.kind, g.info_hash, g.release_title, g.status, g.created_at
FROM grabs g
JOIN wanted_items w ON w.id = g.wanted_item_id
ORDER BY g.created_at, g.id;

-- +goose Down
DROP INDEX idx_grab_events_series;
DROP INDEX idx_grab_events_created;
DROP TABLE grab_events;
