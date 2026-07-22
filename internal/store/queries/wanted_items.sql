-- name: ListWantedItems :many
SELECT *
FROM wanted_items
WHERE series_id = ?
ORDER BY number;

-- name: CreateWantedItem :one
INSERT INTO wanted_items (series_id, kind, number, title, have)
VALUES (?, ?, ?, ?, ?)
RETURNING *;

-- name: SetWantedItemHave :exec
UPDATE wanted_items SET have = ? WHERE id = ?;
