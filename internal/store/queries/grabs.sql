-- name: UpsertGrab :one
INSERT INTO grabs (wanted_item_id, info_hash, release_title, status)
VALUES (?, ?, ?, ?)
ON CONFLICT (wanted_item_id) DO UPDATE SET
    info_hash     = excluded.info_hash,
    release_title = excluded.release_title,
    status        = excluded.status,
    created_at    = datetime('now'),
    -- A re-grab is a fresh download; the previous attempt's stamps and import
    -- error must not count.
    missing_since = NULL,
    stalled_since = NULL,
    last_error    = NULL
RETURNING *;

-- name: ListGrabsByTitle :many
SELECT g.*
FROM grabs g
JOIN wanted_items w ON w.id = g.wanted_item_id
WHERE w.series_id = ?
ORDER BY g.created_at DESC;

-- name: ListGrabsByInfoHash :many
-- One release's rows, in episode order: the group the importer settles together.
-- item_in_library rides along because it is what makes an import a replacement (#97);
-- format and year ride along because the library target routes and names on them (#198).
-- The three grab row shapes stay parallel: a retry converts between two of them.
SELECT
    g.id, g.wanted_item_id, g.info_hash, g.release_title, g.status,
    g.missing_since, g.stalled_since, g.last_error,
    w.number AS item_number,
    w.kind   AS item_kind,
    w.in_library AS item_in_library,
    s.id     AS title_id,
    s.title  AS title_name,
    s.format AS title_format,
    s.year   AS title_year
FROM grabs g
JOIN wanted_items w ON w.id = g.wanted_item_id
JOIN series s ON s.id = w.series_id
WHERE g.info_hash = ?
ORDER BY w.number;

-- name: GetGrabByID :one
SELECT
    g.id, g.wanted_item_id, g.info_hash, g.release_title, g.status,
    g.missing_since, g.stalled_since, g.last_error,
    w.number AS item_number,
    w.kind   AS item_kind,
    w.in_library AS item_in_library,
    s.id     AS title_id,
    s.title  AS title_name,
    s.format AS title_format,
    s.year   AS title_year
FROM grabs g
JOIN wanted_items w ON w.id = g.wanted_item_id
JOIN series s ON s.id = w.series_id
WHERE g.id = ?;

-- name: ListGrabsByStatus :many
SELECT
    g.id, g.wanted_item_id, g.info_hash, g.release_title, g.status,
    g.missing_since, g.stalled_since, g.last_error,
    w.number AS item_number,
    w.kind   AS item_kind,
    w.in_library AS item_in_library,
    s.id     AS title_id,
    s.title  AS title_name,
    s.format AS title_format,
    s.year   AS title_year
FROM grabs g
JOIN wanted_items w ON w.id = g.wanted_item_id
JOIN series s ON s.id = w.series_id
WHERE g.status = ?
ORDER BY g.info_hash;

-- name: ListOpenGrabs :many
-- Open mirrors the importer's scan set: grabbed plus deferred.
SELECT
    g.id, g.wanted_item_id, g.info_hash, g.release_title, g.status,
    g.missing_since, g.stalled_since, g.last_error, g.created_at,
    w.number AS item_number,
    w.kind   AS item_kind,
    s.id     AS title_id,
    s.title  AS title_name,
    s.format AS title_format
FROM grabs g
JOIN wanted_items w ON w.id = g.wanted_item_id
JOIN series s ON s.id = w.series_id
WHERE g.status IN ('grabbed', 'import_deferred')
ORDER BY g.created_at DESC, g.id DESC;

-- name: ListGrabInfoHashes :many
-- Every hash any grab row points at, settled ones included: a torrent nothing
-- references is what "unmatched" means, not one nothing useful references.
SELECT DISTINCT info_hash FROM grabs WHERE info_hash != '';

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

-- name: SetGrabStalledSince :exec
-- Same clock and format as missing_since above.
UPDATE grabs SET stalled_since = ? WHERE id = ?;

-- name: SetGrabLastError :exec
UPDATE grabs SET last_error = ? WHERE id = ?;
