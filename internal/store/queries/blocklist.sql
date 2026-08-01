-- NOTE: keep comments in this file ASCII-only. sqlc's sqlite codegen miscounts
-- byte vs. rune offsets and a multi-byte character in a doc comment silently
-- truncates the emitted SQL. See CLAUDE.md.

-- name: UpsertBlocklistEntry :one
-- A repeat failure of the same release bumps the existing row so the escalating
-- expiry can see the count; hash, reason and expiry take the latest attempt's.
INSERT INTO release_blocklist (series_id, info_hash, release_title, normalized_title, reason, blocked_until)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (series_id, normalized_title) DO UPDATE SET
    info_hash     = excluded.info_hash,
    release_title = excluded.release_title,
    reason        = excluded.reason,
    blocked_until = excluded.blocked_until,
    failures      = release_blocklist.failures + 1,
    updated_at    = datetime('now')
RETURNING *;

-- name: ListActiveBlocklist :many
-- What decide must refuse right now. A NULL blocked_until is permanent.
SELECT *
FROM release_blocklist
WHERE series_id = ? AND (blocked_until IS NULL OR blocked_until > ?)
ORDER BY updated_at DESC;

-- name: ListBlocklistBySeries :many
SELECT *
FROM release_blocklist
WHERE series_id = ?
ORDER BY updated_at DESC;

-- name: GetBlocklistEntry :one
SELECT *
FROM release_blocklist
WHERE id = ?
LIMIT 1;

-- name: DeleteBlocklistEntry :execrows
-- Scoped to the series so an unblock cannot reach another series' entry.
DELETE FROM release_blocklist
WHERE id = ? AND series_id = ?;
