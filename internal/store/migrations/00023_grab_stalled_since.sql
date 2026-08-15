-- +goose Up
-- Records when a grabbed torrent was first seen stalled with nothing downloaded.
-- A stall is only a verdict once it persists, so the first sighting is what the
-- importer measures the timeout from. NULL means not currently stalled at 0%,
-- which is also what a torrent that resumed writes back.
ALTER TABLE grabs ADD COLUMN stalled_since TEXT;

-- +goose Down
ALTER TABLE grabs DROP COLUMN stalled_since;
