-- +goose Up
-- Records when a grabbed torrent was first absent from the download client's
-- report. A torrent removed out-of-band is simply omitted from the response, so
-- persisting the first sighting is what lets the importer tell one absent scan
-- from a torrent that is genuinely gone. NULL means currently reported.
ALTER TABLE grabs ADD COLUMN missing_since TEXT;

-- +goose Down
ALTER TABLE grabs DROP COLUMN missing_since;
