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

-- name: SetBlocklistExpiry :exec
-- NULL is permanent. Separate from the upsert because the ladder is keyed on the
-- failure count the upsert only reports after it has written.
UPDATE release_blocklist SET blocked_until = ?, updated_at = datetime('now') WHERE id = ?;

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

-- name: DeleteBlocklistEntry :execrows
-- Scoped to the series so an unblock cannot reach another series' entry.
DELETE FROM release_blocklist
WHERE id = ? AND series_id = ?;

-- name: DeleteBlocklistBySeries :execrows
-- Bulk unblock for one series. Scoped like the single-entry delete.
DELETE FROM release_blocklist
WHERE series_id = ?;

-- name: DeleteExpiredBlocklistBySeries :execrows
-- A permanent entry (NULL blocked_until) is never expired, so it survives this.
DELETE FROM release_blocklist
WHERE series_id = ? AND blocked_until IS NOT NULL AND blocked_until <= ?;

-- name: DeleteAllBlocklist :execrows
-- Library-wide clear: an environmental fault does not respect series
-- boundaries, so neither does recovery from one.
DELETE FROM release_blocklist;

-- name: CountActiveBlocklist :one
-- The failure-memory summary: how much is currently being skipped, and how far
-- it has spread. A NULL blocked_until is permanent.
SELECT COUNT(*) AS entries, COUNT(DISTINCT series_id) AS series
FROM release_blocklist
WHERE blocked_until IS NULL OR blocked_until > ?;
