-- name: GetSeasonCache :one
SELECT *
FROM season_cache
WHERE provider = ? AND season = ? AND year = ?
LIMIT 1;

-- name: UpsertSeasonCache :exec
INSERT INTO season_cache (provider, season, year, raw, fetched_at)
VALUES (?, ?, ?, ?, datetime('now'))
ON CONFLICT (provider, season, year) DO UPDATE SET
    raw        = excluded.raw,
    fetched_at = excluded.fetched_at;

-- name: ListSeasonCache :many
SELECT *
FROM season_cache
WHERE provider = ?
ORDER BY year, season;
