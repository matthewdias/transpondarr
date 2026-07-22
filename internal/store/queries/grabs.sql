-- name: UpsertGrab :one
INSERT INTO grabs (wanted_item_id, info_hash, release_title, status)
VALUES (?, ?, ?, ?)
ON CONFLICT (wanted_item_id) DO UPDATE SET
    info_hash     = excluded.info_hash,
    release_title = excluded.release_title,
    status        = excluded.status,
    created_at    = datetime('now'),
    -- A re-grab is a fresh download: any missing-from-client stamp left by the
    -- previous attempt must not count against it.
    missing_since = NULL
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
    g.missing_since,
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

-- name: SetGrabMissingSince :exec
-- Stamps (or, with NULL, clears) the moment the download client stopped
-- reporting this torrent. The importer stamps only when the value is unset, so
-- the grace window is measured from the *first* absence, and clears the stamp as
-- soon as the hash is reported again. The timestamp is supplied by the caller,
-- in SQLite's datetime('now') format, so writing and comparing it use one clock.
-- NOTE: keep this comment ASCII-only. sqlc's sqlite codegen miscounts byte vs.
-- rune offsets and silently truncates the emitted SQL when a doc comment
-- contains multi-byte characters (e.g. an em dash).
UPDATE grabs SET missing_since = ? WHERE id = ?;
