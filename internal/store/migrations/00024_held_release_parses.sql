-- +goose Up
-- The parse of the release a wanted item holds (issue #185). Cutoff Unmet
-- decides membership by scoring every held release on every request, and
-- parsing a title costs ~113x what scoring the parse does -- so the tab was at
-- its most expensive exactly when a library was healthy and nothing qualified.
-- Only the parse is cached: it is a pure function of the stored title, where a
-- score depends on the profile and would have to be invalidated whenever one
-- was edited. release_title is the parse's own key rather than a record of it:
-- the read joins on it, so a row left behind by a superseded release simply
-- does not match, and the one writer of wanted_items.held_release_title needs
-- no knowledge of this table. One row per item, overwritten in place, so the
-- table stays bounded by wanted_items and needs no retention. No backfill --
-- SQL cannot run the parser, so the rows are filled as the listing reads them.
-- parser_version is the second half of the key: a title alone does not identify
-- a parse, since the parser that read it can change under a stored row that
-- would otherwise match forever.
CREATE TABLE held_release_parses (
    wanted_item_id INTEGER NOT NULL PRIMARY KEY REFERENCES wanted_items (id) ON DELETE CASCADE,
    release_title  TEXT NOT NULL,
    parser_version INTEGER NOT NULL,
    parsed         TEXT NOT NULL
);

-- +goose Down
DROP TABLE held_release_parses;
