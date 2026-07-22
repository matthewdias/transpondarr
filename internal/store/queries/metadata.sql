-- name: GetCachedMetadata :one
SELECT *
FROM metadata_cache
WHERE provider = ? AND provider_id = ?
LIMIT 1;

-- name: UpsertMetadata :exec
INSERT INTO metadata_cache (provider, provider_id, status, format, episode_count, title, raw, fetched_at)
VALUES (?, ?, ?, ?, ?, ?, ?, datetime('now'))
ON CONFLICT (provider, provider_id) DO UPDATE SET
    status        = excluded.status,
    format        = excluded.format,
    episode_count = excluded.episode_count,
    title         = excluded.title,
    raw           = excluded.raw,
    fetched_at    = excluded.fetched_at;
