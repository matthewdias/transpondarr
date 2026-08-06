-- +goose Up
-- What the last pass decided about one wanted item (issue #181). A refusal lives
-- only in memory for the length of a pass, so "the queue got here and declined"
-- was indistinguishable from "nothing has looked at this yet". One row per item,
-- overwritten in place rather than appended: this is written on the hot path of
-- every pass, so the table stays bounded by wanted_items and needs no retention.
-- No secondary index: the primary key is the only access path, joined 1:1 from
-- the item it describes, and nothing scans it. No backfill either -- a missing
-- row correctly means no pass has spoken.
CREATE TABLE pass_outcomes (
    wanted_item_id INTEGER NOT NULL PRIMARY KEY REFERENCES wanted_items (id) ON DELETE CASCADE,
    outcome        TEXT NOT NULL,
    source         TEXT NOT NULL,
    release_title  TEXT NOT NULL DEFAULT '',
    detail         TEXT NOT NULL DEFAULT '',
    held_until     TEXT,
    recorded_at    TEXT NOT NULL
);

-- +goose Down
DROP TABLE pass_outcomes;
