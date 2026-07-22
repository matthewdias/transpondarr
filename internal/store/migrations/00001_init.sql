-- +goose Up
-- Content-type-agnostic core: a Title holds WantedItems. An episode is one
-- item; a movie (added later) is a Title with a single item — no schema change
-- to the pipeline required.
CREATE TABLE series (
    id         INTEGER PRIMARY KEY,
    anilist_id INTEGER UNIQUE,
    title      TEXT    NOT NULL,
    format     TEXT    NOT NULL DEFAULT 'TV',
    monitored  INTEGER NOT NULL DEFAULT 1,
    created_at TEXT    NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE wanted_items (
    id        INTEGER PRIMARY KEY,
    series_id INTEGER NOT NULL REFERENCES series (id) ON DELETE CASCADE,
    kind      TEXT    NOT NULL DEFAULT 'episode',
    number    INTEGER,
    title     TEXT,
    have      INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX idx_wanted_items_series ON wanted_items (series_id);

-- +goose Down
DROP TABLE wanted_items;
DROP TABLE series;
