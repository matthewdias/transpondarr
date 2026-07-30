-- name: ListSeries :many
SELECT *
FROM series
ORDER BY title;

-- name: ListSeriesWithProgress :many
SELECT
    s.*,
    COUNT(w.id)                            AS total_items,
    CAST(COALESCE(SUM(w.have), 0) AS INTEGER) AS have_items
FROM series s
LEFT JOIN wanted_items w ON w.series_id = s.id
GROUP BY s.id
ORDER BY s.title;

-- name: GetSeries :one
SELECT *
FROM series
WHERE id = ?
LIMIT 1;

-- name: GetSeriesByAnilistID :one
SELECT *
FROM series
WHERE anilist_id = ?
LIMIT 1;

-- name: CreateSeries :one
INSERT INTO series (anilist_id, title, format, monitored)
VALUES (?, ?, ?, ?)
RETURNING *;

-- name: SetSeriesMonitored :exec
UPDATE series SET monitored = ? WHERE id = ?;

-- name: SetSeriesPinnedGroup :execrows
-- NULL clears the pin; execrows lets the handler 404 an unknown series.
UPDATE series SET pinned_group = ? WHERE id = ?;

-- name: ListSeriesDueAiringSync :many
-- Monitored series whose broadcast schedule has never been synced or has gone
-- stale. A finished title's aired times are immutable, so it waits on the long
-- cutoff while anything still moving waits on the short one. A series with no
-- cache row has unknown status and deliberately rides the short cutoff: unknown
-- is likelier a transient anomaly than a finished title, and the cost is one
-- tail request per short TTL. Never-synced series sort first; the limit bounds
-- how much of the request budget one pass can burn.
SELECT s.*
FROM series s
LEFT JOIN metadata_cache m ON m.provider = 'anilist' AND m.provider_id = s.anilist_id
WHERE s.monitored = 1
  AND s.anilist_id IS NOT NULL
  AND (
      s.airing_synced_at IS NULL
      OR s.airing_synced_at < CASE
             WHEN m.status IN ('FINISHED', 'CANCELLED') THEN ?
             ELSE ?
         END
  )
ORDER BY s.airing_synced_at IS NOT NULL, s.airing_synced_at
LIMIT ?;

-- name: SetSeriesAiringSyncedAt :exec
-- Guarded on the value read at selection: a refresh that cleared the stamp
-- mid-sync must win, so the next airing pass re-pages the grown series.
UPDATE series SET airing_synced_at = datetime('now')
WHERE id = ? AND airing_synced_at IS ?;

-- name: ClearSeriesAiringSyncedAt :exec
-- Forces the next airing pass to re-page full history, so items inserted after
-- the last sync get air dates without waiting out the series' TTL.
UPDATE series SET airing_synced_at = NULL WHERE id = ?;

-- name: ListTrackedNextAiring :many
-- Discovery overlay rows: every series carrying an AniList id, joined to its
-- next item scheduled after the given instant. airing_synced_at rides along so
-- the caller can tell "synced, nothing upcoming" from "never synced".
SELECT
    s.id,
    s.anilist_id,
    s.airing_synced_at,
    w.number AS next_number,
    w.airs_at AS next_airs_at
FROM series s
LEFT JOIN wanted_items w ON w.id = (
    SELECT w2.id
    FROM wanted_items w2
    WHERE w2.series_id = s.id
      AND w2.airs_at IS NOT NULL
      AND w2.airs_at > ?
    ORDER BY w2.airs_at
    LIMIT 1
)
WHERE s.anilist_id IS NOT NULL;

-- name: ListSeriesDueMetadataRefresh :many
-- Monitored series whose cached title snapshot is missing or stale under the
-- status-aware TTL policy. Only a finished title with a known episode count
-- earns the long cutoff; anything moving, unknown, or empty rides the short
-- one, mirroring the freshness rule in metadata.Cached. Never-fetched series
-- sort first; the limit bounds how much of the request budget one pass burns.
SELECT s.*
FROM series s
LEFT JOIN metadata_cache m ON m.provider = 'anilist' AND m.provider_id = s.anilist_id
WHERE s.monitored = 1
  AND s.anilist_id IS NOT NULL
  AND (
      m.provider_id IS NULL
      OR m.fetched_at < CASE
             WHEN m.status IN ('FINISHED', 'CANCELLED') AND m.episode_count IS NOT NULL THEN ?
             ELSE ?
         END
  )
ORDER BY m.fetched_at IS NOT NULL, m.fetched_at
LIMIT ?;
