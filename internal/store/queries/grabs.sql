-- name: UpsertGrab :one
INSERT INTO grabs (wanted_item_id, info_hash, release_title, status)
VALUES (?, ?, ?, ?)
ON CONFLICT (wanted_item_id) DO UPDATE SET
    info_hash     = excluded.info_hash,
    release_title = excluded.release_title,
    status        = excluded.status,
    created_at    = datetime('now')
RETURNING *;

-- name: ListGrabsBySeries :many
SELECT g.*
FROM grabs g
JOIN wanted_items w ON w.id = g.wanted_item_id
WHERE w.series_id = ?
ORDER BY g.created_at DESC;

-- name: ListGrabsByInfoHash :many
SELECT *
FROM grabs
WHERE info_hash = ?;

-- name: ListGrabsByStatus :many
SELECT
    g.id, g.wanted_item_id, g.info_hash, g.release_title, g.status,
    w.number AS item_number,
    w.kind   AS item_kind,
    s.id     AS series_id,
    s.title  AS series_title,
    s.format AS series_format
FROM grabs g
JOIN wanted_items w ON w.id = g.wanted_item_id
JOIN series s ON s.id = w.series_id
WHERE g.status = ?
ORDER BY g.info_hash;

-- name: SetGrabStatus :exec
UPDATE grabs SET status = ? WHERE id = ?;
