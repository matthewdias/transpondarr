-- +goose Up
-- Cache for seasonal browse charts. A season is a ~50-title query result, not a
-- title, so it does not fit metadata_cache's (provider, provider_id) shape. One
-- row per season holds the whole chart as a JSON blob, refreshed by the
-- season-refresh job rather than on page view.
CREATE TABLE season_cache (
    provider   TEXT    NOT NULL,           -- e.g. 'anilist'
    season     TEXT    NOT NULL,           -- WINTER / SPRING / SUMMER / FALL
    year       INTEGER NOT NULL,
    raw        TEXT    NOT NULL,           -- JSON: []metadata.SeasonEntry
    fetched_at TEXT    NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (provider, season, year)
);

-- +goose Down
DROP TABLE season_cache;
