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
