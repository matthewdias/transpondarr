-- +goose Up
-- Records when a grabbed torrent was first absent from the download client's
-- report. A torrent removed out-of-band is omitted from the response, so
-- persisting the first absence is what lets the importer distinguish one absent
-- scan from a torrent that is gone. NULL means currently reported.
ALTER TABLE grabs ADD COLUMN missing_since TEXT;

-- +goose Down
ALTER TABLE grabs DROP COLUMN missing_since;
