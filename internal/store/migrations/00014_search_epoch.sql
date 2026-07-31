-- +goose Up
-- The sweep's write guard (issue #100). next_search_at could not distinguish
-- "unchanged" from "reset while I was searching": a reset writes NULL, which is
-- also the value a due series most often carries, so the stale backoff won the
-- race it was meant to lose. A counter only ever moves forward.
ALTER TABLE series ADD COLUMN search_epoch INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE series DROP COLUMN search_epoch;
