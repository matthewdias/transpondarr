-- +goose Up
-- One-time data fix on live instances, not a schema change. Rows deferred by the
-- old code were never examined -- it deferred every directory payload sight
-- unseen -- and import_deferred is terminal, so they would keep that verdict
-- forever. Requeueing hands them to payload resolution exactly once; each then
-- settles as imported, deferred again, or failed, so none can loop.
UPDATE grabs SET status = 'grabbed', missing_since = NULL WHERE status = 'import_deferred';

-- +goose Down
-- Irreversible: these rows are indistinguishable from ones deferred afterwards.
SELECT 1;
