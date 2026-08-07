-- +goose NO TRANSACTION
-- +goose Up
-- series.anilist_id becomes the (provider, provider_id) pair metadata_cache is
-- already keyed on (issue #74), so the primary key stops naming its upstream.
--
-- This has to rebuild the table rather than ALTER it: `anilist_id INTEGER
-- UNIQUE` carries an implicit index that no DROP INDEX can remove, and it is
-- over-strict on the pair (MAL 123 alongside AniList 123). A CHECK cannot be
-- bolted on afterwards either -- SQLite has no ADD CONSTRAINT, and ADD COLUMN
-- validates existing rows, which no constant default can satisfy for both a
-- tracked and an untracked row.
--
-- The rebuild is dangerous because series has three ON DELETE CASCADE children
-- and foreign keys are enforced on every pooled connection (store/db.go): with
-- them on, DROP TABLE series empties the user's library. The whole script is
-- therefore one statement block, which goose hands to a single Exec and
-- database/sql runs on a single pooled connection -- the only way to guarantee
-- that the per-connection PRAGMA and the DROP meet. NO TRANSACTION is required
-- because PRAGMA foreign_keys is a silent no-op inside one; the explicit BEGIN
-- keeps the rebuild itself atomic.
-- +goose StatementBegin
PRAGMA foreign_keys = off;
BEGIN;
CREATE TABLE series_new (
    id                 INTEGER PRIMARY KEY,
    provider           TEXT,
    provider_id        INTEGER,
    title              TEXT    NOT NULL,
    format             TEXT    NOT NULL DEFAULT 'TV',
    monitored          INTEGER NOT NULL DEFAULT 1,
    created_at         TEXT    NOT NULL DEFAULT (datetime('now')),
    quality_profile_id INTEGER NOT NULL DEFAULT 1,
    airing_synced_at   TEXT,
    pinned_group       TEXT,
    last_searched_at   TEXT,
    search_backoff     INTEGER NOT NULL DEFAULT 0,
    next_search_at     TEXT,
    pin_delay_hours    INTEGER,
    search_epoch       INTEGER NOT NULL DEFAULT 0,
    UNIQUE (provider, provider_id),
    -- Half an identity names an id space with nothing in it, or an id with no
    -- space to read it in; both are the ambiguity this change removes.
    CHECK ((provider IS NULL) = (provider_id IS NULL))
);
INSERT INTO series_new (
    id, provider, provider_id, title, format, monitored, created_at,
    quality_profile_id, airing_synced_at, pinned_group, last_searched_at,
    search_backoff, next_search_at, pin_delay_hours, search_epoch
)
SELECT
    id,
    CASE WHEN anilist_id IS NULL THEN NULL ELSE 'anilist' END,
    anilist_id, title, format, monitored, created_at,
    quality_profile_id, airing_synced_at, pinned_group, last_searched_at,
    search_backoff, next_search_at, pin_delay_hours, search_epoch
FROM series;
DROP TABLE series;
ALTER TABLE series_new RENAME TO series;
-- Step 10 of SQLite's ALTER recipe, made to actually abort: a bare
-- PRAGMA foreign_key_check only *returns* offending rows, and a migration runs
-- through Exec, which discards them -- so orphaned children would commit
-- silently. Landing the count in a CHECK turns them into a failed statement.
CREATE TABLE fk_violations (n INTEGER NOT NULL CHECK (n = 0));
INSERT INTO fk_violations (n) SELECT count(*) FROM pragma_foreign_key_check;
DROP TABLE fk_violations;
COMMIT;
PRAGMA foreign_keys = on;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
PRAGMA foreign_keys = off;
BEGIN;
CREATE TABLE series_old (
    id                 INTEGER PRIMARY KEY,
    anilist_id         INTEGER UNIQUE,
    title              TEXT    NOT NULL,
    format             TEXT    NOT NULL DEFAULT 'TV',
    monitored          INTEGER NOT NULL DEFAULT 1,
    created_at         TEXT    NOT NULL DEFAULT (datetime('now')),
    quality_profile_id INTEGER NOT NULL DEFAULT 1,
    airing_synced_at   TEXT,
    pinned_group       TEXT,
    last_searched_at   TEXT,
    search_backoff     INTEGER NOT NULL DEFAULT 0,
    next_search_at     TEXT,
    pin_delay_hours    INTEGER,
    search_epoch       INTEGER NOT NULL DEFAULT 0
);
-- A row keyed on any other provider has no anilist_id to go back to, so it
-- downgrades to an untracked title rather than claiming an id in the wrong space.
INSERT INTO series_old (
    id, anilist_id, title, format, monitored, created_at,
    quality_profile_id, airing_synced_at, pinned_group, last_searched_at,
    search_backoff, next_search_at, pin_delay_hours, search_epoch
)
SELECT
    id,
    CASE WHEN provider = 'anilist' THEN provider_id ELSE NULL END,
    title, format, monitored, created_at,
    quality_profile_id, airing_synced_at, pinned_group, last_searched_at,
    search_backoff, next_search_at, pin_delay_hours, search_epoch
FROM series;
DROP TABLE series;
ALTER TABLE series_old RENAME TO series;
CREATE TABLE fk_violations (n INTEGER NOT NULL CHECK (n = 0));
INSERT INTO fk_violations (n) SELECT count(*) FROM pragma_foreign_key_check;
DROP TABLE fk_violations;
COMMIT;
PRAGMA foreign_keys = on;
-- +goose StatementEnd
