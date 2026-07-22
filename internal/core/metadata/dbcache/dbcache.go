// Package dbcache implements metadata.Cache on top of the SQLite metadata_cache
// table (via the sqlc layer). It keeps the provider adapter DB-free: the adapter
// speaks HTTP, this speaks SQL, and metadata.Cached composes them.
package dbcache

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/metadata"
	"github.com/matthewdias/transpondarr/internal/store/db"
)

// fetchedAtLayout matches SQLite's datetime('now') output (UTC, no zone).
const fetchedAtLayout = "2006-01-02 15:04:05"

// Cache is a metadata.Cache backed by the metadata_cache table.
type Cache struct {
	q *db.Queries
}

// New builds a Cache over the given query set.
func New(q *db.Queries) *Cache { return &Cache{q: q} }

// Get returns the cached snapshot for provider+id, if present. A malformed row
// (unparseable JSON or timestamp) is treated as a miss rather than an error, so
// a poisoned cache entry degrades to a re-fetch instead of a hard failure.
func (c *Cache) Get(ctx context.Context, provider string, id int64) (metadata.CachedTitle, time.Time, bool, error) {
	row, err := c.q.GetCachedMetadata(ctx, db.GetCachedMetadataParams{Provider: provider, ProviderID: id})
	if errors.Is(err, sql.ErrNoRows) {
		return metadata.CachedTitle{}, time.Time{}, false, nil
	}
	if err != nil {
		return metadata.CachedTitle{}, time.Time{}, false, err
	}

	var snap metadata.CachedTitle
	if err := json.Unmarshal([]byte(row.Raw), &snap); err != nil {
		return metadata.CachedTitle{}, time.Time{}, false, nil
	}
	fetchedAt, err := time.Parse(fetchedAtLayout, row.FetchedAt)
	if err != nil {
		return metadata.CachedTitle{}, time.Time{}, false, nil
	}
	return snap, fetchedAt, true, nil
}

// Put upserts a snapshot, mirroring the queryable columns the refresh job filters
// on out of the JSON blob.
func (c *Cache) Put(ctx context.Context, provider string, id int64, snap metadata.CachedTitle) error {
	raw, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	return c.q.UpsertMetadata(ctx, db.UpsertMetadataParams{
		Provider:     provider,
		ProviderID:   id,
		Status:       nullString(snap.Title.Status),
		Format:       nullString(string(snap.Title.Format)),
		EpisodeCount: nullInt(snap.Title.Episodes),
		Title:        nullString(snap.Title.Titles.Romaji),
		Raw:          string(raw),
	})
}

func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

func nullInt(n int) sql.NullInt64 {
	if n == 0 {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(n), Valid: true}
}

var _ metadata.Cache = (*Cache)(nil)
