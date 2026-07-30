-- +goose Up
-- Per-series cadence for the scheduled search sweep (issue #100). The backoff
-- delay itself has no clean SQLite expression, so next_search_at is precomputed
-- in Go and this column is only read: NULL means due now, which is also what a
-- freshly added or reset series starts at.
ALTER TABLE series ADD COLUMN last_searched_at TEXT;
ALTER TABLE series ADD COLUMN search_backoff INTEGER NOT NULL DEFAULT 0;
ALTER TABLE series ADD COLUMN next_search_at TEXT;

-- How long the sweep waits for the pinned group before taking someone else's
-- release (issue #62). NULL means "use the global default"; 0 means no wait.
ALTER TABLE series ADD COLUMN pin_delay_hours INTEGER;

-- +goose Down
ALTER TABLE series DROP COLUMN pin_delay_hours;
ALTER TABLE series DROP COLUMN next_search_at;
ALTER TABLE series DROP COLUMN search_backoff;
ALTER TABLE series DROP COLUMN last_searched_at;
