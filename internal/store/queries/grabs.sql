-- name: UpsertGrab :one
INSERT INTO grabs (wanted_item_id, info_hash, release_title, status)
VALUES (?, ?, ?, ?)
ON CONFLICT (wanted_item_id) DO UPDATE SET
    info_hash     = excluded.info_hash,
    release_title = excluded.release_title,
    status        = excluded.status,
    created_at    = datetime('now'),
    -- A re-grab is a fresh download; the previous attempt's stamp and import
    -- error must not count.
    missing_since = NULL,
    last_error    = NULL
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
    g.missing_since, g.last_error,
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
-- Every status but grabbed is settled, so a stale import error never survives
-- a transition.
UPDATE grabs SET status = ?, last_error = NULL WHERE id = ?;

-- name: SetGrabMissingSince :exec
-- The caller supplies the timestamp in SQLite's datetime('now') format, so
-- writing and comparing it use one clock.
-- NOTE: keep comments here ASCII-only. sqlc's sqlite codegen miscounts byte vs.
-- rune offsets and silently truncates the emitted SQL on a multi-byte character.
UPDATE grabs SET missing_since = ? WHERE id = ?;

-- name: SetGrabLastError :exec
UPDATE grabs SET last_error = ? WHERE id = ?;
