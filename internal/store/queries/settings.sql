-- name: ListSettings :many
SELECT key, value
FROM settings;

-- name: GetSetting :one
SELECT value
FROM settings
WHERE key = ?
LIMIT 1;

-- name: UpsertSetting :exec
INSERT INTO settings (key, value, updated_at)
VALUES (?, ?, datetime('now'))
ON CONFLICT (key) DO UPDATE SET
    value      = excluded.value,
    updated_at = datetime('now');
