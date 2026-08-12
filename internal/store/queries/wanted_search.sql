-- name: ListSeriesDueWantedSearch :many
-- Monitored series with something actually searchable right now: an item still
-- wanted (never grabbed, or a grab that failed), itself monitored, whose
-- broadcast has happened or was never published. Air dates are nullable by
-- design, so a missing one must read as searchable rather than as "not yet".
-- Series never searched sort first; the limit bounds how much of the indexer
-- budget one pass can burn.
-- NOTE: keep comments here ASCII-only. sqlc's sqlite codegen miscounts byte vs.
-- rune offsets and silently truncates the emitted SQL on a multi-byte character.
-- Movies are excluded here rather than in Go (#208): decide cannot match one
-- yet, so next_search_at would never advance and the movie would permanently
-- hold a slot at the head of this LIMIT-ordered queue, burning one indexer
-- search per pass. Revert with #209.
SELECT s.*
FROM series s
WHERE s.monitored = 1
  AND s.format <> 'MOVIE'
  AND (s.next_search_at IS NULL OR s.next_search_at <= ?)
  AND EXISTS (
      SELECT 1
      FROM wanted_items w
      LEFT JOIN grabs g ON g.wanted_item_id = w.id
      WHERE w.series_id = s.id
        AND w.in_library = 0
        AND w.monitored = 1
        AND (g.wanted_item_id IS NULL OR g.status = 'failed')
        AND (w.airs_at IS NULL OR w.airs_at <= ?)
  )
ORDER BY s.next_search_at IS NOT NULL, s.next_search_at
LIMIT ?;

-- name: ListSeriesWithWantedItems :many
-- Monitored series with something worth grabbing right now, ignoring search
-- cadence. The feed poll issues no indexer request per series -- one request
-- answers for every series at once -- so the budget the sweep's LIMIT protects
-- does not apply here. The wanted half is deliberately the sweep's predicate,
-- character for character, so both entry points agree on what is grabbable.
-- The upgrade half is the deliberate divergence (#97): a complete series is
-- worth re-examining only against a page that cost nothing, so upgrades ride
-- the feed alone. Item monitoring gates both halves (#188), the upgrade one
-- included, or an unmonitored held item makes its series feed-due every poll.
-- Score versus cutoff is decided in Go, under the one profile snapshot that
-- also scores the candidates.
-- NOTE: keep comments here ASCII-only. sqlc's sqlite codegen miscounts byte vs.
-- rune offsets and silently truncates the emitted SQL on a multi-byte character.
SELECT s.*
FROM series s
JOIN quality_profiles qp ON qp.id = s.quality_profile_id
WHERE s.monitored = 1
  AND (
      EXISTS (
          SELECT 1
          FROM wanted_items w
          LEFT JOIN grabs g ON g.wanted_item_id = w.id
          WHERE w.series_id = s.id
            AND w.in_library = 0
            AND w.monitored = 1
            AND (g.wanted_item_id IS NULL OR g.status = 'failed')
            AND (w.airs_at IS NULL OR w.airs_at <= ?)
      )
      OR (
          qp.upgrades_enabled = 1
          AND EXISTS (
              SELECT 1
              FROM wanted_items w
              JOIN grabs g ON g.wanted_item_id = w.id
              WHERE w.series_id = s.id
                AND w.in_library = 1
                AND w.monitored = 1
                AND w.held_release_title != ''
                AND g.status IN ('imported', 'failed')
          )
      )
  )
ORDER BY s.id;

-- name: ListBackedOffSeriesWantedInWindow :many
-- Series the sweep is postponing that had a broadcast inside a window: what the
-- feed poll resets after it detects a gap in its own coverage. Already-due
-- series are excluded because a reset buys them nothing and would spend one of
-- the bounded slots. The wanted predicate is the sweep's, character for
-- character, item monitoring included. Furthest-postponed first, since the
-- ladder would keep those
-- waiting longest, and the LIMIT holds the reset to one sweep pass' throughput.
-- NOTE: keep comments here ASCII-only. sqlc's sqlite codegen miscounts byte vs.
-- rune offsets and silently truncates the emitted SQL on a multi-byte character.
SELECT s.*
FROM series s
WHERE s.monitored = 1
  AND s.next_search_at IS NOT NULL
  AND s.next_search_at > ?
  AND EXISTS (
      SELECT 1
      FROM wanted_items w
      LEFT JOIN grabs g ON g.wanted_item_id = w.id
      WHERE w.series_id = s.id
        AND w.in_library = 0
        AND w.monitored = 1
        AND (g.wanted_item_id IS NULL OR g.status = 'failed')
        AND w.airs_at IS NOT NULL AND w.airs_at >= ? AND w.airs_at < ?
  )
ORDER BY s.next_search_at DESC, s.id
LIMIT ?;

-- name: ListWantedItemsWithGrabState :many
-- One grab per item (UNIQUE) keeps the join 1:1, so the sweep can tell an
-- in-flight episode from a wanted one in a single query per series.
SELECT w.*, g.status AS grab_status
FROM wanted_items w
LEFT JOIN grabs g ON g.wanted_item_id = w.id
WHERE w.series_id = ?
ORDER BY w.number;

-- name: SetSeriesSearchState :execrows
-- Guarded on the epoch read at selection: a reset that landed mid-sweep (the
-- series grew, was re-monitored, or was repinned) must win over the backoff
-- computed against the stale state. Guarding on next_search_at could not do
-- that -- a reset writes NULL, which is also what a due series usually already
-- held. execrows is what lets the caller see that its write lost.
UPDATE series
SET last_searched_at = ?, search_backoff = ?, next_search_at = ?
WHERE id = ? AND search_epoch = ?;

-- name: ResetSeriesSearchState :exec
-- Puts a series back at the front of the queue: due now, no accumulated backoff.
-- Bumping the epoch is what makes an in-flight sweep's write lose.
UPDATE series
SET search_backoff = 0, next_search_at = NULL, search_epoch = search_epoch + 1
WHERE id = ?;

-- name: ResetAllSeriesSearchState :exec
-- The whole library back at the front of the queue. Notify-only rehearses a pass
-- that settles nothing, so every rehearsed series climbs the backoff ladder to
-- its daily cap; switching automation on has to undo that or the first real
-- sweep for a rehearsed series is up to a day away. The due query's LIMIT paces
-- the resulting queue, so this is a reset, not a burst.
UPDATE series
SET search_backoff = 0, next_search_at = NULL, search_epoch = search_epoch + 1;
