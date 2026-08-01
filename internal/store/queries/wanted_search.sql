-- name: ListSeriesDueWantedSearch :many
-- Monitored series with something actually searchable right now: an item still
-- wanted (never grabbed, or a grab that failed) whose broadcast has happened or
-- was never published. Air dates are nullable by design, so a missing one must
-- read as searchable rather than as "not yet". Series never searched sort first;
-- the limit bounds how much of the indexer budget one pass can burn.
-- NOTE: keep comments here ASCII-only. sqlc's sqlite codegen miscounts byte vs.
-- rune offsets and silently truncates the emitted SQL on a multi-byte character.
SELECT s.*
FROM series s
WHERE s.monitored = 1
  AND (s.next_search_at IS NULL OR s.next_search_at <= ?)
  AND EXISTS (
      SELECT 1
      FROM wanted_items w
      LEFT JOIN grabs g ON g.wanted_item_id = w.id
      WHERE w.series_id = s.id
        AND w.have = 0
        AND (g.wanted_item_id IS NULL OR g.status = 'failed')
        AND (w.airs_at IS NULL OR w.airs_at <= ?)
  )
ORDER BY s.next_search_at IS NOT NULL, s.next_search_at
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
