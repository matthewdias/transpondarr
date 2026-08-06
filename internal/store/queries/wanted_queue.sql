-- The cross-series wanted queue (issue #150): Missing and Cutoff Unmet, both
-- keyset-paginated. A missing set is unbounded -- a fresh library add can put
-- hundreds of items in it at once -- so neither list may load whole.
-- NOTE: keep comments in this file ASCII-only. sqlc's sqlite codegen miscounts
-- byte vs. rune offsets and silently truncates the emitted SQL on a multi-byte
-- character. See CLAUDE.md.

-- name: ListMissingSeriesPage :many
-- The Missing tab's pagination unit is the series, so a group can never split
-- across a page boundary. The wanted half is the sweep's predicate character
-- for character (the EXISTS body of ListSeriesDueWantedSearch), which is what
-- keeps this page honest about what automation will go after; an in-flight
-- grab is absent by construction, being Activity's to show. Groups are ordered
-- newest missing broadcast first, all-undated series last: COALESCE sorts a
-- null air date below every timestamp, and lexicographic compare on the one
-- stored layout is chronological. The keyset lives in HAVING because it binds
-- on the aggregate; a first page passes a sentinel above every stored value so
-- one query serves every page. The count is the whole group even when the
-- handler caps the items it returns for one.
SELECT s.id, s.title, s.monitored, s.last_searched_at, s.next_search_at,
       MAX(COALESCE(w.airs_at, '')) AS latest_missing_air,
       COUNT(*)                     AS missing
FROM wanted_items w
JOIN series s ON s.id = w.series_id
LEFT JOIN grabs g ON g.wanted_item_id = w.id
WHERE w.have = 0
  AND (g.wanted_item_id IS NULL OR g.status = 'failed')
  AND (? = 1 OR s.monitored = 1)
  AND (? = 1 OR w.airs_at IS NULL OR w.airs_at <= ?)
GROUP BY s.id
HAVING MAX(COALESCE(w.airs_at, '')) < ? OR (MAX(COALESCE(w.airs_at, '')) = ? AND s.id > ?)
ORDER BY MAX(COALESCE(w.airs_at, '')) DESC, s.id
LIMIT ?;

-- name: ListMissingItemsBySeries :many
-- The items behind one page of groups. Same wanted predicate and unaired filter
-- as the series page, so a group and its items are computed from one reading of
-- the world. Number ascends within a series deliberately: a back catalogue
-- drains forwards, and episodes enumerate forwards however their dates fall.
SELECT w.*,
       g.status        AS grab_status,
       g.release_title AS grab_release_title,
       g.last_error    AS grab_last_error
FROM wanted_items w
LEFT JOIN grabs g ON g.wanted_item_id = w.id
WHERE w.series_id IN (sqlc.slice('series_ids'))
  AND w.have = 0
  AND (g.wanted_item_id IS NULL OR g.status = 'failed')
  AND (? = 1 OR w.airs_at IS NULL OR w.airs_at <= ?)
ORDER BY w.series_id, w.number;

-- name: ListCutoffSeriesPage :many
-- Candidate groups for Cutoff Unmet: series on an upgrading profile holding
-- anything the upgrade pool could act on. The status set is the sweep's pool
-- (imported, failed -- see loadSweepItems) plus grabbed, which is an upgrade
-- already in flight and worth showing as such. import_deferred is deliberately
-- out: that item's fix is the Activity queue's, and a grab from here would
-- overwrite the deferred row and orphan its payload. Whether a held release
-- actually scores below the cutoff needs the parser and is settled in Go, so a
-- series here may contribute no group and the caller scans on. Ordered by title
-- -- this listing is an inventory, not a queue, so alphabetical reads best --
-- with the id tie-break ascending and a zero cursor as the natural top.
SELECT s.id, s.title, s.monitored,
       qp.id           AS profile_id,
       qp.name         AS profile_name,
       qp.cutoff_score AS profile_cutoff_score
FROM series s
JOIN quality_profiles qp ON qp.id = s.quality_profile_id
WHERE qp.upgrades_enabled = 1
  AND (? = 1 OR s.monitored = 1)
  AND EXISTS (
      SELECT 1
      FROM wanted_items w
      JOIN grabs g ON g.wanted_item_id = w.id
      WHERE w.series_id = s.id
        AND w.have = 1
        AND w.held_release_title != ''
        AND g.status IN ('imported', 'failed', 'grabbed')
  )
  AND (s.title > ? OR (s.title = ? AND s.id > ?))
ORDER BY s.title, s.id
LIMIT ?;

-- name: ListCutoffItemsBySeries :many
-- Every rateable held item behind one page of candidate groups; scoring and the
-- cutoff test happen in Go under the one profile snapshot per series. The grab
-- join is inner and its status set matches the series page's, so a group's
-- items are exactly what made its series a candidate.
SELECT w.*,
       g.status        AS grab_status,
       g.release_title AS grab_release_title,
       g.last_error    AS grab_last_error
FROM wanted_items w
JOIN grabs g ON g.wanted_item_id = w.id
WHERE w.series_id IN (sqlc.slice('series_ids'))
  AND w.have = 1
  AND w.held_release_title != ''
  AND g.status IN ('imported', 'failed', 'grabbed')
ORDER BY w.series_id, w.number;

-- name: ListActiveBlocklistCounts :many
-- How many releases each series is currently refusing, for the reason column.
-- Per series rather than per item because that is the blocklist's own scope.
-- A NULL blocked_until is permanent.
SELECT series_id, COUNT(*) AS entries
FROM release_blocklist
WHERE blocked_until IS NULL OR blocked_until > ?
GROUP BY series_id;
