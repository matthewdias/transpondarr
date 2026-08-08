-- +goose Up
-- Per-item monitoring (issue #188). monitor_new_from is a numeric cut rather
-- than a mode: the sites that must honour it (the airing gap-fill and refresh)
-- create items with no air date, so "future only" has nothing to re-evaluate
-- there, and a number records the decision as taken rather than re-derived.
-- NULL means monitor nothing new. Both defaults preserve today's behaviour.
ALTER TABLE wanted_items ADD COLUMN monitored INTEGER NOT NULL DEFAULT 1;
ALTER TABLE series ADD COLUMN monitor_new_from INTEGER DEFAULT 1;

-- +goose Down
ALTER TABLE wanted_items DROP COLUMN monitored;
ALTER TABLE series DROP COLUMN monitor_new_from;
