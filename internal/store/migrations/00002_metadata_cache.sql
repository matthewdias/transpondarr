-- +goose Up
-- Read-through cache for external metadata providers (AniList first). Hybrid
-- shape: a few queryable columns the refresh job filters/sorts on, plus a raw
-- JSON blob holding the provider-agnostic snapshot so new fields don't churn the
-- schema. Keyed by (provider, provider_id) — one AniList media entry per row.
CREATE TABLE metadata_cache (
    provider      TEXT    NOT NULL,          -- e.g. 'anilist'
    provider_id   INTEGER NOT NULL,          -- provider's media id
    status        TEXT,                      -- RELEASING / FINISHED / ...
    format        TEXT,
    episode_count INTEGER,
    title         TEXT,                      -- best-effort display title (romaji)
    raw           TEXT    NOT NULL,          -- JSON: metadata.CachedTitle snapshot
    fetched_at    TEXT    NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (provider, provider_id)
);

-- Supports the future refresh job: "RELEASING titles not fetched since <cutoff>".
CREATE INDEX idx_metadata_cache_refresh ON metadata_cache (provider, status, fetched_at);

-- +goose Down
DROP TABLE metadata_cache;
