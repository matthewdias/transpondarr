-- +goose Up
-- Why the last import attempt for a still-grabbed torrent did not land (source
-- path not accessible, library Place failed). NULL means no failed attempt; any
-- status transition clears it, since every status but grabbed is settled.
ALTER TABLE grabs ADD COLUMN last_error TEXT;

-- +goose Down
ALTER TABLE grabs DROP COLUMN last_error;
