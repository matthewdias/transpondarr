-- name: ListWantedItems :many
SELECT *
FROM wanted_items
WHERE series_id = ?
ORDER BY number;

-- name: CreateWantedItem :one
INSERT INTO wanted_items (series_id, kind, number, title, in_library, monitored)
VALUES (?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: UpsertWantedItem :execrows
-- DO NOTHING keeps refresh from ever clobbering an existing item's in_library,
-- title or monitored; the row count tells the caller whether the series grew.
INSERT INTO wanted_items (series_id, kind, number, title, in_library, monitored)
VALUES (?, ?, ?, ?, 0, ?)
ON CONFLICT (series_id, kind, number) DO NOTHING;

-- name: GetWantedItemByNumber :one
-- One read answers exists / had / already spoken for, which is the whole guard
-- on placing a payload file for an item no grab row claimed. UNIQUE
-- (wanted_item_id) on grabs keeps the join 1:1.
SELECT w.*, g.status AS grab_status
FROM wanted_items w
LEFT JOIN grabs g ON g.wanted_item_id = w.id
WHERE w.series_id = ? AND w.kind = ? AND w.number = ?;

-- name: SetWantedItemsMonitored :execrows
-- The bulk state-setter behind the Episodes table and the Wanted page. Unknown
-- ids are simply not matched: for a state-setter a missing item is vacuous
-- rather than erroneous, so a concurrent series delete must not cost a caller
-- its whole selection.
UPDATE wanted_items SET monitored = ? WHERE id IN (sqlc.slice('ids'));

-- name: ListSeriesIDsForUnmonitoredItems :many
-- The series a re-monitor will actually change, so the cadence reset lands once
-- per series and only where something moved. Restricted to monitored = 0 on
-- purpose: re-monitoring an already-monitored selection changes nothing, and
-- resetting there would discard accumulated backoff for free. Read before the
-- update, in the same transaction, since the update reports only a row count.
SELECT DISTINCT series_id
FROM wanted_items
WHERE id IN (sqlc.slice('ids')) AND monitored = 0;

-- name: SetWantedItemInLibrary :exec
UPDATE wanted_items SET in_library = ? WHERE id = ?;

-- name: SetWantedItemHeld :exec
-- The one write point for held identity: what the library holds, and which
-- release put it there, so an upgrade has something to score against.
UPDATE wanted_items SET in_library = ?, held_release_title = ? WHERE id = ?;

-- name: ListCalendarItems :many
-- Stored timestamps are UTC in one fixed layout, so lexicographic range
-- compare is chronological. One grab per item (UNIQUE) keeps the join 1:1.
SELECT w.*,
       s.title         AS series_title,
       s.monitored     AS series_monitored,
       g.status        AS grab_status,
       g.release_title AS grab_release_title,
       g.last_error    AS grab_last_error
FROM wanted_items w
JOIN series s ON s.id = w.series_id
LEFT JOIN grabs g ON g.wanted_item_id = w.id
WHERE w.airs_at IS NOT NULL AND w.airs_at >= ? AND w.airs_at < ?
ORDER BY w.airs_at, s.title, w.number;

-- name: ListUnscheduledSeries :many
-- Monitored series still missing an episode the provider gives no air date
-- for, so the calendar can surface them instead of silently omitting them.
SELECT DISTINCT s.id, s.title
FROM series s
JOIN wanted_items w ON w.series_id = s.id
WHERE s.monitored = 1 AND w.in_library = 0 AND w.airs_at IS NULL
ORDER BY s.title;

-- name: UpsertWantedItemAiring :exec
-- Creating the item matters for a null-count long-runner: the schedule is the
-- only source that knows its episodes exist. Only airs_at moves on conflict, so
-- a stored monitored flag survives every resync.
INSERT INTO wanted_items (series_id, kind, number, airs_at, monitored)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (series_id, kind, number) DO UPDATE SET airs_at = excluded.airs_at;
