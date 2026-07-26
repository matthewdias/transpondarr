// Package refresh grows a tracked series as its provider metadata moves: a
// releasing title whose episode count rises (or arrives, having been unknown at
// add time) gains the missing wanted items on the next pass. It never touches
// existing rows, so a refresh cannot clobber have or re-grab anything.
package refresh

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/domain"
	"github.com/matthewdias/transpondarr/internal/core/metadata"
	"github.com/matthewdias/transpondarr/internal/store"
	"github.com/matthewdias/transpondarr/internal/store/db"
)

// seriesPerPass bounds how much of the request budget one pass can spend. Series
// due a refresh sort never-fetched first, so a series whose cache row was lost
// is picked up ahead of routine re-checks.
const seriesPerPass = 5

// Service upserts wanted items from re-fetched title metadata.
type Service struct {
	store    *store.Store
	provider metadata.Provider
	log      *slog.Logger
}

// New builds a Service over the shared cached provider. Sharing matters twice:
// the adapter underneath carries the rate limiter, and the cache's fetched_at
// stamp is what marks a series as no longer due.
func New(st *store.Store, provider metadata.Provider, log *slog.Logger) *Service {
	return &Service{store: st, provider: provider, log: log}
}

// RefreshOnce re-fetches metadata for every series due one, and is what the job
// runner calls. One series' error never costs the rest their refresh.
func (s *Service) RefreshOnce(ctx context.Context) error {
	due, err := s.due(ctx)
	if err != nil {
		return fmt.Errorf("list series due a metadata refresh: %w", err)
	}

	var errs []error
	for _, series := range due {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := s.refreshSeries(ctx, series); err != nil {
			errs = append(errs, fmt.Errorf("series %d: %w", series.ID, err))
		}
	}
	return errors.Join(errs...)
}

// due lists the series whose title snapshot is missing or stale, pacing each
// status by the same TTL policy the title cache uses.
func (s *Service) due(ctx context.Context) ([]db.Series, error) {
	now := time.Now()
	cutoff := func(status string) string {
		return store.FormatTimestamp(now.Add(-metadata.TTLFor(status)))
	}
	return s.store.Q.ListSeriesDueMetadataRefresh(ctx, db.ListSeriesDueMetadataRefreshParams{
		FetchedAt:   cutoff("FINISHED"),
		FetchedAt_2: cutoff("RELEASING"),
		Limit:       seriesPerPass,
	})
}

// refreshSeries upserts one series' items from a re-fetch. When the series
// grew, its airing stamp is cleared so the next airing pass re-pages full
// history — without it, an item born after the last sync (a tail-only resync
// never revisits aired history) could wait out the long TTL for its air date.
func (s *Service) refreshSeries(ctx context.Context, series db.Series) error {
	_, items, err := s.provider.GetTitle(ctx, series.AnilistID.Int64)
	if err != nil {
		return fmt.Errorf("fetch metadata: %w", err)
	}

	var inserted int64
	for _, it := range items {
		n, err := s.store.Q.UpsertWantedItem(ctx, db.UpsertWantedItemParams{
			SeriesID: series.ID,
			Kind:     string(domain.KindEpisode),
			Number:   sql.NullInt64{Int64: int64(it.Number), Valid: true},
			Title:    nullString(it.Name),
		})
		if err != nil {
			return fmt.Errorf("upsert item %d: %w", it.Number, err)
		}
		inserted += n
	}

	if inserted > 0 {
		if err := s.store.Q.ClearSeriesAiringSyncedAt(ctx, series.ID); err != nil {
			return fmt.Errorf("clear airing stamp: %w", err)
		}
		s.log.Info("series grew", "series", series.ID, "new_items", inserted)
	}
	return nil
}

func nullString(v string) sql.NullString {
	if v == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: v, Valid: true}
}
