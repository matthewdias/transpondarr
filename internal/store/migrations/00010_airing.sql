-- +goose Up
-- Broadcast times, so the scheduled sweep can search for items that have
-- actually aired instead of blind-polling every wanted item. Nullable because
-- AniList's schedule coverage thins out badly before ~2015: every consumer must
-- treat "no air date" as normal rather than as an error.
ALTER TABLE wanted_items ADD COLUMN airs_at TEXT;

-- Drives "monitored series never synced, or stale" without a second table.
ALTER TABLE series ADD COLUMN airing_synced_at TEXT;

-- Duplicates should not exist (AddSeries expands 1..N exactly once), but a live
-- upgrade must not fail on one. The survivor is the row carrying state — had
-- first, then grabbed, then oldest — since deleting it would cascade its grab away.
DELETE FROM wanted_items WHERE id IN (
    SELECT id FROM (
        SELECT w.id,
               ROW_NUMBER() OVER (
                   PARTITION BY w.series_id, w.kind, w.number
                   ORDER BY w.have DESC,
                            (SELECT COUNT(*) FROM grabs g WHERE g.wanted_item_id = w.id) DESC,
                            w.id
               ) AS dup_rank
        FROM wanted_items w
        WHERE w.number IS NOT NULL
    )
    WHERE dup_rank > 1
);

-- Required by the refresh upsert: ON CONFLICT needs this identity to target.
CREATE UNIQUE INDEX idx_wanted_items_identity ON wanted_items (series_id, kind, number);

-- +goose Down
DROP INDEX idx_wanted_items_identity;
ALTER TABLE series DROP COLUMN airing_synced_at;
ALTER TABLE wanted_items DROP COLUMN airs_at;
