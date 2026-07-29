-- name: ListWantedItems :many
SELECT *
FROM wanted_items
WHERE series_id = ?
ORDER BY number;

-- name: CreateWantedItem :one
INSERT INTO wanted_items (series_id, kind, number, title, have)
VALUES (?, ?, ?, ?, ?)
RETURNING *;

-- name: UpsertWantedItem :execrows
-- DO NOTHING keeps refresh from ever clobbering an existing item's have or
-- title; the row count tells the caller whether the series actually grew.
INSERT INTO wanted_items (series_id, kind, number, title, have)
VALUES (?, ?, ?, ?, 0)
ON CONFLICT (series_id, kind, number) DO NOTHING;

-- name: SetWantedItemHave :exec
UPDATE wanted_items SET have = ? WHERE id = ?;

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
WHERE s.monitored = 1 AND w.have = 0 AND w.airs_at IS NULL
ORDER BY s.title;

-- name: UpsertWantedItemAiring :exec
-- Creating the item matters for a null-count long-runner: the schedule is the
-- only source that knows its episodes exist. Only airs_at moves on conflict.
INSERT INTO wanted_items (series_id, kind, number, airs_at)
VALUES (?, ?, ?, ?)
ON CONFLICT (series_id, kind, number) DO UPDATE SET airs_at = excluded.airs_at;
