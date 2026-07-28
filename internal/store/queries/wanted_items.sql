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

-- name: UpsertWantedItemAiring :exec
-- Creating the item matters for a null-count long-runner: the schedule is the
-- only source that knows its episodes exist. Only airs_at moves on conflict.
INSERT INTO wanted_items (series_id, kind, number, airs_at)
VALUES (?, ?, ?, ?)
ON CONFLICT (series_id, kind, number) DO UPDATE SET airs_at = excluded.airs_at;
