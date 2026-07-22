-- name: CreateSession :exec
INSERT INTO sessions (token, username, expires_at)
VALUES (?, ?, ?);

-- name: GetSession :one
SELECT username
FROM sessions
WHERE token = ? AND expires_at > datetime('now')
LIMIT 1;

-- name: DeleteSession :exec
DELETE FROM sessions
WHERE token = ?;

-- name: DeleteSessionsForUser :exec
DELETE FROM sessions
WHERE username = ?;

-- name: DeleteExpiredSessions :exec
DELETE FROM sessions
WHERE expires_at <= datetime('now');
