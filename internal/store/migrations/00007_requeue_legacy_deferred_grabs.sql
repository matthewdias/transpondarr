-- +goose Up
-- One-time data fix, not a schema change.
--
-- Before this release the importer deferred *every* directory payload without
-- looking inside it, and 'import_deferred' is terminal — so a single episode
-- that happened to ship inside a folder (with subtitles, an nfo, a sample) was
-- stranded: never imported, its item stuck showing "downloading" forever. The
-- importer now resolves a directory payload down to its one episode file at
-- completion time, but it only ever examines outstanding grabs, so rows already
-- written as 'import_deferred' would keep their old verdict forever.
--
-- Putting them back to 'grabbed' hands them to the new resolution path exactly
-- once more. Each then settles on its own: imported if the folder holds one
-- identifiable episode, deferred again if it is a real batch, or failed (item
-- back to wanted) if the torrent is no longer in the download client. No row can
-- loop, because every outcome except 'grabbed' is settled and 'grabbed' means
-- the torrent is still downloading.
UPDATE grabs SET status = 'grabbed', missing_since = NULL WHERE status = 'import_deferred';

-- +goose Down
-- Irreversible by nature: the rows this touched are indistinguishable from ones
-- deferred afterwards, and re-deferring them would strand them again. Rolling
-- back the schema is a no-op here.
SELECT 1;
