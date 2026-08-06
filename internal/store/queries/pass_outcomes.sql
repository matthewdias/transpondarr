-- What the last pass decided about one wanted item (issue #181).
-- NOTE: keep comments in this file ASCII-only. sqlc's sqlite codegen miscounts
-- byte vs. rune offsets and a multi-byte character in a doc comment silently
-- truncates the emitted SQL. See CLAUDE.md.

-- name: UpsertPassOutcome :exec
-- One row per item, replaced in place. Every column takes the new pass's value
-- so an outcome carrying no hold clears a stale held_until, rather than leaving
-- a closed pin window attached to a decision that no longer mentions it.
INSERT INTO pass_outcomes (wanted_item_id, outcome, source, release_title, detail, held_until, recorded_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (wanted_item_id) DO UPDATE SET
    outcome       = excluded.outcome,
    source        = excluded.source,
    release_title = excluded.release_title,
    detail        = excluded.detail,
    held_until    = excluded.held_until,
    recorded_at   = excluded.recorded_at;

-- name: GetPassOutcome :one
-- One item's stored outcome. The listing reads these through a join; this exists
-- for tests and for a single-item lookup.
SELECT * FROM pass_outcomes WHERE wanted_item_id = ?;
