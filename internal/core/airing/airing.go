// Package airing keeps per-item broadcast times in step with the metadata
// provider, creating the wanted items a schedule names as it goes — for a
// long-runner whose episode total AniList never publishes, the schedule is the
// only source that knows those episodes exist. It is background work rather
// than part of GetTitle because paging a full schedule costs one request per
// 50 episodes: unremarkable off the request path, unacceptable behind a user
// action against a ~30 req/min budget.
package airing

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
// due for a sync sort never-synced first, so a newly added title is picked up on
// the next tick rather than queued behind a backlog of routine refreshes.
const seriesPerPass = 5

// Service syncs broadcast schedules into wanted_items.airs_at.
type Service struct {
	store    *store.Store
	provider metadata.Provider
	log      *slog.Logger
}

// New builds a Service over the shared provider. Sharing matters: the provider
// carries the rate limiter, so a private instance would double the request rate.
func New(st *store.Store, provider metadata.Provider, log *slog.Logger) *Service {
	return &Service{store: st, provider: provider, log: log}
}

// SyncOnce fetches schedules for every series due one, and is what the job runner
// calls. A provider that publishes no schedule is a supported configuration, not
// a failure. One series' error never costs the rest their sync.
func (s *Service) SyncOnce(ctx context.Context) error {
	airing, ok := s.provider.(metadata.AiringProvider)
	if !ok {
		return nil
	}

	due, err := s.due(ctx)
	if err != nil {
		return fmt.Errorf("list series due an airing sync: %w", err)
	}

	var errs []error
	for _, series := range due {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := s.syncSeries(ctx, airing, series); err != nil {
			errs = append(errs, fmt.Errorf("series %d: %w", series.ID, err))
		}
	}
	return errors.Join(errs...)
}

// due lists the series whose schedule has never been synced or has gone stale,
// pacing each status by the same TTL policy the title cache uses.
func (s *Service) due(ctx context.Context) ([]db.Series, error) {
	now := time.Now()
	cutoff := func(status string) sql.NullString {
		return sql.NullString{String: store.FormatTimestamp(now.Add(-metadata.TTLFor(status))), Valid: true}
	}
	return s.store.Q.ListSeriesDueAiringSync(ctx, db.ListSeriesDueAiringSyncParams{
		AiringSyncedAt:   cutoff("FINISHED"),
		AiringSyncedAt_2: cutoff("RELEASING"),
		Limit:            seriesPerPass,
	})
}

// syncSeries writes one series' schedule, then stamps it as synced. The stamp is
// what stops a title AniList has no schedule for (its coverage thins out badly
// before ~2015) from being re-asked every tick forever.
func (s *Service) syncSeries(ctx context.Context, airing metadata.AiringProvider, series db.Series) error {
	// A series synced before has its aired history already; only the not-yet-aired
	// tail can still move, and that is 1-2 pages instead of one per 50 episodes.
	notYetAired := series.AiringSyncedAt.Valid

	schedule, err := airing.GetSchedule(ctx, series.AnilistID.Int64, notYetAired)
	if err != nil {
		return fmt.Errorf("fetch schedule: %w", err)
	}

	for _, a := range schedule {
		if err := s.store.Q.UpsertWantedItemAiring(ctx, db.UpsertWantedItemAiringParams{
			SeriesID: series.ID,
			Kind:     string(domain.KindEpisode),
			Number:   sql.NullInt64{Int64: int64(a.Number), Valid: true},
			AirsAt:   sql.NullString{String: store.FormatTimestamp(a.AirsAt), Valid: true},
		}); err != nil {
			return fmt.Errorf("upsert airing for item %d: %w", a.Number, err)
		}
	}

	// Guarded on the stamp read at selection: if the metadata refresh cleared it
	// mid-sync (the series grew), the clear wins and the next pass re-pages.
	if err := s.store.Q.SetSeriesAiringSyncedAt(ctx, db.SetSeriesAiringSyncedAtParams{
		ID:             series.ID,
		AiringSyncedAt: series.AiringSyncedAt,
	}); err != nil {
		return fmt.Errorf("stamp synced: %w", err)
	}
	s.log.Debug("airing schedule synced", "series", series.ID, "airings", len(schedule), "tail_only", notYetAired)
	return nil
}
