-- +goose Up
-- Records when the download client first reported a grabbed torrent stalled
-- with nothing downloaded. A stall is only a verdict once it persists, so that
-- first report is what the importer measures the timeout from. NULL means not
-- currently stalled at 0%, which is also what a torrent that resumed writes back.
ALTER TABLE grabs ADD COLUMN stalled_since TEXT;

-- +goose Down
ALTER TABLE grabs DROP COLUMN stalled_since;
