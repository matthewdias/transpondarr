// Package airing keeps per-item broadcast times in step with the metadata
// provider, creating the wanted items a schedule names (and the ones it skips)
// as it goes — for a long-runner whose episode total AniList never publishes,
// the schedule is the only source that knows those episodes exist. Paging one
// is background work rather than part of GetTitle because it costs a request
// per page: unremarkable off the request path, unacceptable behind a user
// action against a ~30 req/min budget. GetTitle carries a single in-band page
// for the add; everything past it is here.
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

	// One transaction, opened after the fetch so no write lock is held across the
	// network: a never-synced long-runner writes thousands of rows, and in
	// autocommit each is its own fsync — ~60x the wall clock, spent holding off
	// the importer and grab paths on busy_timeout.
	tx, err := s.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful Commit
	q := s.store.Q.WithTx(tx)

	for _, a := range schedule {
		if err := q.UpsertWantedItemAiring(ctx, db.UpsertWantedItemAiringParams{
			SeriesID: series.ID,
			Kind:     string(domain.KindEpisode),
			Number:   sql.NullInt64{Int64: int64(a.Number), Valid: true},
			AirsAt:   sql.NullString{String: store.FormatTimestamp(a.AirsAt), Valid: true},
		}); err != nil {
			return fmt.Errorf("upsert airing for item %d: %w", a.Number, err)
		}
	}

	var filled int64
	for _, n := range skipped(schedule, !notYetAired) {
		rows, err := q.UpsertWantedItem(ctx, db.UpsertWantedItemParams{
			SeriesID: series.ID,
			Kind:     string(domain.KindEpisode),
			Number:   sql.NullInt64{Int64: int64(n), Valid: true},
		})
		if err != nil {
			return fmt.Errorf("create item %d for a skipped schedule entry: %w", n, err)
		}
		filled += rows
	}
	// A filled item has no air date, so it is exactly what airedSince cannot see.
	if filled > 0 {
		if err := q.ResetSeriesSearchState(ctx, series.ID); err != nil {
			return fmt.Errorf("reset search cadence: %w", err)
		}
	}

	// Guarded on the stamp read at selection: if the metadata refresh cleared it
	// mid-sync (the series grew), the clear wins and the next pass re-pages.
	if err := q.SetSeriesAiringSyncedAt(ctx, db.SetSeriesAiringSyncedAtParams{
		ID:             series.ID,
		AiringSyncedAt: series.AiringSyncedAt,
	}); err != nil {
		return fmt.Errorf("stamp synced: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	s.log.Debug("airing schedule synced", "series", series.ID, "airings", len(schedule), "tail_only", notYetAired)
	return nil
}

// skipped lists the item numbers a schedule implies but never names. fromOne
// widens the fill to the whole numbering, which only a full fetch owns: a tail
// is a partial view, so it fills gaps inside its own span instead.
func skipped(schedule []metadata.Airing, fromOne bool) []int {
	if len(schedule) == 0 {
		return nil
	}
	known := make(map[int]bool, len(schedule))
	lo, hi := schedule[0].Number, schedule[0].Number
	for _, a := range schedule {
		known[a.Number] = true
		lo, hi = min(lo, a.Number), max(hi, a.Number)
	}
	if fromOne {
		lo = 1
	}
	var out []int
	for n := lo; n <= hi; n++ {
		if !known[n] {
			out = append(out, n)
		}
	}
	return out
}
