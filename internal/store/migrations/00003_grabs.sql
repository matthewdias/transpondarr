-- +goose Up
-- Records that a release was grabbed (handed to the download client) for a wanted
-- item. A batch release inserts one row per covered item, all sharing the
-- torrent's info hash — the client-agnostic identifier the pipeline keys on.
-- wanted_items.have is deliberately NOT flipped here: a grab means "downloading",
-- and only a successful library import marks an item as had. One active grab per
-- item (UNIQUE), so re-grabbing an item replaces its grab.
CREATE TABLE grabs (
    id             INTEGER PRIMARY KEY,
    wanted_item_id INTEGER NOT NULL UNIQUE REFERENCES wanted_items (id) ON DELETE CASCADE,
    info_hash      TEXT    NOT NULL,
    release_title  TEXT    NOT NULL DEFAULT '',
    status         TEXT    NOT NULL DEFAULT 'grabbed',
    created_at     TEXT    NOT NULL DEFAULT (datetime('now'))
);

-- Import looks grabs up by the completed torrent's info hash.
CREATE INDEX idx_grabs_info_hash ON grabs (info_hash);

-- +goose Down
DROP TABLE grabs;
