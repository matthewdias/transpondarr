-- name: ListTitles :many
SELECT *
FROM series
ORDER BY title;

-- name: ListTitlesWithProgress :many
-- Progress is measured against what is being pursued (#188): monitored and
-- already broadcast, numerator and denominator carrying the identical filter so
-- a held unaired item cannot push a series past its own total. A null air date
-- reads as aired, as everywhere else here, and so does a movie: its date is the
-- theatrical premiere rather than the moment it becomes acquirable, so an
-- announced film is being waited on now and must not read "Nothing aired yet"
-- from the day it gains one. monitored_items rides along so a zero
-- denominator can name its own cause: nothing aired yet, or nothing monitored.
-- NOTE: keep comments here ASCII-only. sqlc's sqlite codegen miscounts byte vs.
-- rune offsets and silently truncates the emitted SQL on a multi-byte character.
SELECT
    s.*,
    COUNT(w.id)                            AS total_items,
    CAST(COALESCE(SUM(w.monitored = 1), 0) AS INTEGER) AS monitored_items,
    CAST(COALESCE(SUM(
        w.monitored = 1 AND (w.kind = 'movie' OR w.airs_at IS NULL OR w.airs_at <= ?)
    ), 0) AS INTEGER)                      AS tracked_items,
    CAST(COALESCE(SUM(
        w.in_library = 1 AND w.monitored = 1 AND (w.kind = 'movie' OR w.airs_at IS NULL OR w.airs_at <= ?)
    ), 0) AS INTEGER)                      AS in_library_items
FROM series s
LEFT JOIN wanted_items w ON w.series_id = s.id
GROUP BY s.id
ORDER BY s.title;

-- name: ListMovieItemStates :many
-- Format guarantees a film one wanted item (#208), so a per-title list can carry
-- that item's own state -- which reads its grab, and so cannot come from the
-- aggregate above. One grab per item (UNIQUE) keeps the join 1:1.
-- NOTE: keep comments here ASCII-only. sqlc's sqlite codegen miscounts byte vs.
-- rune offsets and silently truncates the emitted SQL on a multi-byte character.
SELECT w.series_id,
       w.in_library,
       g.status        AS grab_status,
       g.release_title AS grab_release_title,
       g.last_error    AS grab_last_error
FROM wanted_items w
JOIN series s ON s.id = w.series_id
LEFT JOIN grabs g ON g.wanted_item_id = w.id
WHERE s.format = 'MOVIE'
ORDER BY w.series_id;

-- name: GetTitle :one
SELECT *
FROM series
WHERE id = ?
LIMIT 1;

-- name: GetTitleByProviderID :one
-- The pair is the identity: the same id in two provider spaces is two titles.
SELECT *
FROM series
WHERE provider = ? AND provider_id = ?
LIMIT 1;

-- name: CreateTitle :one
-- monitor_new_from is left to its schema default: an omitted sqlc params field
-- would write NULL, which reads as "monitor nothing new".
INSERT INTO series (provider, provider_id, title, format, monitored, year)
VALUES (?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: SetTitleYear :exec
-- Guarded in SQL so no future caller can bypass it: a transient upstream null
-- must never erase a stored year, which the naming layer reads and where zero
-- means "not on record".
UPDATE series SET year = ? WHERE id = ? AND ? > 0;

-- name: SetTitleMonitorNewFrom :exec
-- The cut every later create site reads: an item numbered at or above it is
-- created monitored. NULL monitors nothing new, and no mode writes one -- with
-- nothing able to edit the cut afterwards, a null would be permanent.
UPDATE series SET monitor_new_from = ? WHERE id = ?;

-- name: SetTitleMonitored :exec
UPDATE series SET monitored = ? WHERE id = ?;

-- name: DeleteTitle :execrows
DELETE FROM series WHERE id = ?;

-- name: SetTitlePinnedGroup :execrows
-- NULL clears the pin; execrows lets the handler 404 an unknown series. The
-- delay rides along because it is meaningless without a group to wait for, so
-- PUT-replacing one must replace the other.
UPDATE series SET pinned_group = ?, pin_delay_hours = ? WHERE id = ?;

-- name: ListTitlesDueAiringSync :many
-- Monitored series whose broadcast schedule has never been synced or has gone
-- stale. A finished title's aired times are immutable, so it waits on the long
-- cutoff while anything still moving waits on the short one. A series with no
-- cache row has unknown status and deliberately rides the short cutoff: unknown
-- is likelier a transient anomaly than a finished title, and the cost is one
-- tail request per short TTL. Never-synced series sort first; the limit bounds
-- how much of the request budget one pass can burn. Scoped to one provider
-- because the id this hands to it is only meaningful in that provider's space.
SELECT s.*
FROM series s
LEFT JOIN metadata_cache m ON m.provider = s.provider AND m.provider_id = s.provider_id
WHERE s.monitored = 1
  AND s.provider = ?
  AND (
      s.airing_synced_at IS NULL
      OR s.airing_synced_at < CASE
             WHEN m.status IN ('FINISHED', 'CANCELLED') THEN ?
             ELSE ?
         END
  )
ORDER BY s.airing_synced_at IS NOT NULL, s.airing_synced_at
LIMIT ?;

-- name: SetTitleAiringSyncedAt :exec
-- Guarded on the value read at selection: a refresh that cleared the stamp
-- mid-sync must win, so the next airing pass re-pages the grown series.
UPDATE series SET airing_synced_at = datetime('now')
WHERE id = ? AND airing_synced_at IS ?;

-- name: ClearTitleAiringSyncedAt :exec
-- Forces the next airing pass to re-page full history, so items inserted after
-- the last sync get air dates without waiting out the series' TTL.
UPDATE series SET airing_synced_at = NULL WHERE id = ?;

-- name: ListTrackedNextAiring :many
-- Discovery overlay rows: every series keyed on the given provider, joined to
-- its next item scheduled after the given instant. airing_synced_at rides along
-- so the caller can tell "synced, nothing upcoming" from "never synced".
SELECT
    s.id,
    s.provider_id,
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
WHERE s.provider = ?;

-- name: ListTitlesDueMetadataRefresh :many
-- Monitored series whose cached title snapshot is missing or stale under the
-- status-aware TTL policy. Only a finished title with a known episode count
-- earns the long cutoff; anything moving, unknown, or empty rides the short
-- one, mirroring the freshness rule in metadata.Cached. Never-fetched series
-- sort first; the limit bounds how much of the request budget one pass burns.
-- Scoped to one provider for the same reason ListTitlesDueAiringSync is.
SELECT s.*
FROM series s
LEFT JOIN metadata_cache m ON m.provider = s.provider AND m.provider_id = s.provider_id
WHERE s.monitored = 1
  AND s.provider = ?
  AND (
      m.provider_id IS NULL
      OR m.fetched_at < CASE
             WHEN m.status IN ('FINISHED', 'CANCELLED') AND m.episode_count IS NOT NULL THEN ?
             ELSE ?
         END
  )
ORDER BY m.fetched_at IS NOT NULL, m.fetched_at
LIMIT ?;
