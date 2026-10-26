-- +goose Up
-- Names a person entered for a title (issue #345), as a JSON array of strings.
-- A column on series rather than a table: every due query is SELECT s.*, so the
-- feed poll and the search sweep read a title's aliases from the row they already
-- loaded, with no query per title and no way to read another title's.
ALTER TABLE series ADD COLUMN aliases TEXT NOT NULL DEFAULT '[]';

-- +goose Down
ALTER TABLE series DROP COLUMN aliases;
