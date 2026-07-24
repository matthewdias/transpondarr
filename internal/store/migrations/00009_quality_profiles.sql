-- +goose Up
-- What a user wants a release to be. Anime inverts Sonarr's weighting: release
-- group is the dominant quality signal, so groups live in their own ordered
-- table below rather than as a tiebreaker column here. resolution_order and
-- hard_excludes are JSON arrays (ordered best-first / axis-value tokens like
-- "hardsub") — small closed sets, unlike groups, which users reorder row-wise.
CREATE TABLE quality_profiles (
    id                INTEGER PRIMARY KEY,
    name              TEXT    NOT NULL UNIQUE,
    is_default        INTEGER NOT NULL DEFAULT 0,
    resolution_order  TEXT    NOT NULL DEFAULT '["1080p","720p","480p"]' CHECK (json_valid(resolution_order)),
    preferred_source  TEXT    NOT NULL DEFAULT '',
    sub_pref          TEXT    NOT NULL DEFAULT '',
    prefer_dual_audio INTEGER NOT NULL DEFAULT 0,
    codec_pref        TEXT    NOT NULL DEFAULT '',
    hard_excludes     TEXT    NOT NULL DEFAULT '[]' CHECK (json_valid(hard_excludes)),
    -- Floor: a candidate scoring below this is ineligible, so the answer can be
    -- "nothing yet" instead of the least-bad thing available.
    min_score         INTEGER NOT NULL DEFAULT 0,
    created_at        TEXT    NOT NULL DEFAULT (datetime('now'))
);

-- Exactly one default profile; it is what new series start on and what the UI
-- refuses to delete.
CREATE UNIQUE INDEX idx_quality_profiles_default ON quality_profiles (is_default) WHERE is_default = 1;

-- Ranked group preference, rank 1 most preferred. A blocked row is the
-- never-take list; its rank carries no meaning.
CREATE TABLE quality_profile_groups (
    id         INTEGER PRIMARY KEY,
    profile_id INTEGER NOT NULL REFERENCES quality_profiles (id) ON DELETE CASCADE,
    rank       INTEGER NOT NULL,
    group_name TEXT    NOT NULL,
    blocked    INTEGER NOT NULL DEFAULT 0,
    UNIQUE (profile_id, group_name)
);

INSERT INTO quality_profiles (id, name, is_default) VALUES (1, 'Default', 1);

-- Existing series keep working with no backfill: everything starts on Default.
-- No REFERENCES clause: SQLite forbids ADD COLUMN with both a foreign key and a
-- non-NULL default while foreign_keys is on (our DSN enforces it). The queries
-- carry the integrity instead: SetSeriesProfile requires the profile to exist,
-- DeleteQualityProfile refuses while any series still points at it.
ALTER TABLE series ADD COLUMN quality_profile_id INTEGER NOT NULL DEFAULT 1;

-- +goose Down
ALTER TABLE series DROP COLUMN quality_profile_id;
DROP TABLE quality_profile_groups;
DROP TABLE quality_profiles;
