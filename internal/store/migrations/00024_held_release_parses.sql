-- +goose Up
-- The parse of a wanted item's held release (issue #185). Cutoff Unmet
-- computes membership by scoring every held release on every request, and
-- parsing a release name costs ~113x what scoring the parse does -- so the tab was at
-- its most expensive exactly when a library was healthy and nothing qualified.
-- Only the parse is cached: it is a pure function of the stored release name, where a
-- score depends on the profile and would have to be invalidated whenever one
-- was edited. release_title is the parse's own key rather than a record of it:
-- the read joins on it, so a parse row left behind by a superseded release simply
-- does not match, and the one writer of wanted_items.held_release_title never
-- touches this table. One parse row per item, overwritten in place, so the
-- table stays bounded by wanted_items and needs no retention. No backfill --
-- SQL cannot run the parser, so the parse rows are filled as the listing reads them.
-- parser_version is the second half of the key: a release name alone does not identify
-- a parse, since the parser that read it can change under a stored parse row that
-- would otherwise match forever.
CREATE TABLE held_release_parses (
    wanted_item_id INTEGER NOT NULL PRIMARY KEY REFERENCES wanted_items (id) ON DELETE CASCADE,
    release_title  TEXT NOT NULL,
    parser_version INTEGER NOT NULL,
    parsed         TEXT NOT NULL
);

-- +goose Down
DROP TABLE held_release_parses;
