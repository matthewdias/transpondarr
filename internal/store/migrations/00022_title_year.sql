-- +goose Up
-- Movies (issue #208). year is 0 rather than NULL for "no year on record",
-- matching metadata.Candidate.Year, so no read site grows a .Valid check.
ALTER TABLE series ADD COLUMN year INTEGER NOT NULL DEFAULT 0;

-- Re-key items a pre-#208 movie add created as episodes. Load-bearing, not
-- tidying: idx_wanted_items_identity is (series_id, kind, number), so a new
-- ('movie', 1) would not conflict with a legacy ('episode', 1) and the first
-- refresh after deploy would silently double every pre-existing movie.
UPDATE wanted_items SET kind = 'movie'
WHERE kind = 'episode'
  AND series_id IN (SELECT id FROM series WHERE format = 'MOVIE');

-- +goose Down
UPDATE wanted_items SET kind = 'episode'
WHERE kind = 'movie'
  AND series_id IN (SELECT id FROM series WHERE format = 'MOVIE');
ALTER TABLE series DROP COLUMN year;
