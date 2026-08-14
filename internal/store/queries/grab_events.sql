-- NOTE: keep comments here ASCII-only. sqlc's sqlite codegen miscounts byte vs.
-- rune offsets and silently truncates the emitted SQL on a multi-byte character.

-- name: AppendGrabEvent :exec
INSERT INTO grab_events (series_id, wanted_item_id, item_number, item_kind, info_hash, release_title, event, detail)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListGrabEventsPage :many
SELECT e.*, s.title AS title_name
FROM grab_events e
JOIN series s ON s.id = e.series_id
ORDER BY e.created_at DESC, e.id DESC
LIMIT ?;

-- name: ListGrabEventsPageBefore :many
-- Keyset cursor on (created_at, id); the timestamp is passed twice because this
-- dialect rejects named params.
SELECT e.*, s.title AS title_name
FROM grab_events e
JOIN series s ON s.id = e.series_id
WHERE e.created_at < ? OR (e.created_at = ? AND e.id < ?)
ORDER BY e.created_at DESC, e.id DESC
LIMIT ?;

-- name: ListTitleGrabEvents :many
SELECT *
FROM grab_events
WHERE series_id = ?
ORDER BY created_at DESC, id DESC;
