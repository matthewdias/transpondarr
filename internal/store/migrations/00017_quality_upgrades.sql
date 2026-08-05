-- +goose Up
-- Upgrades are per-profile opt-in and cutoff-bounded, not a chase: automation
-- re-grabs a held item only while what holds it scores below cutoff_score.
-- Zero means the cutoff is already met, symmetric with a zero min_score meaning
-- no floor, so enabling upgrades does nothing until a landmark is chosen.
ALTER TABLE quality_profiles ADD COLUMN upgrades_enabled INTEGER NOT NULL DEFAULT 0;
ALTER TABLE quality_profiles ADD COLUMN cutoff_score INTEGER NOT NULL DEFAULT 0;
-- The carve-out: a v2/repack of the release we already hold is a fix rather than
-- an upgrade, so it is taken above cutoff too. Inert until upgrades are enabled.
ALTER TABLE quality_profiles ADD COLUMN upgrade_v2_above_cutoff INTEGER NOT NULL DEFAULT 1;

-- What a held item is holding. The grab row cannot answer it: a failed upgrade
-- overwrites release_title with the release that failed, and grabs is UNIQUE per
-- item, so held identity lives beside have.
ALTER TABLE wanted_items ADD COLUMN held_release_title TEXT NOT NULL DEFAULT '';

-- Backfill from the grab that imported the item; the scalar subquery is safe
-- because grabs is UNIQUE (wanted_item_id). A held item whose row was since
-- overwritten by a failed grab backfills to '' and stays outside the upgrade
-- pool until its next import, which is accepted rather than guessed at.
UPDATE wanted_items
SET held_release_title = COALESCE((
        SELECT g.release_title FROM grabs g
        WHERE g.wanted_item_id = wanted_items.id AND g.status = 'imported'
    ), '')
WHERE have = 1;

-- +goose Down
ALTER TABLE wanted_items DROP COLUMN held_release_title;
ALTER TABLE quality_profiles DROP COLUMN upgrade_v2_above_cutoff;
ALTER TABLE quality_profiles DROP COLUMN cutoff_score;
ALTER TABLE quality_profiles DROP COLUMN upgrades_enabled;
