-- +goose Up
-- missing_since records when a grabbed torrent's info hash was first absent from
-- the download client's report. A torrent removed out-of-band (manually, by a
-- seeding rule, by a client reset) is simply omitted from the client's response,
-- which on its own is indistinguishable from a transient blip — persisting the
-- first sighting is what lets the importer tell "absent for one scan" from
-- "genuinely gone" and fail the grab after a grace period.
-- NULL means the torrent is currently reported by the client (the normal case);
-- the importer clears the stamp as soon as the hash reappears, so a restarted
-- client fully recovers instead of limping toward a false failure.
ALTER TABLE grabs ADD COLUMN missing_since TEXT;

-- +goose Down
ALTER TABLE grabs DROP COLUMN missing_since;
