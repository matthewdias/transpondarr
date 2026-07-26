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

-- name: SetWantedItemAirsAt :exec
UPDATE wanted_items
SET airs_at = ?
WHERE series_id = ? AND kind = ? AND number = ?;
