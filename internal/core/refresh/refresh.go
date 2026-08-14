// Package refresh grows a tracked title as its provider metadata moves: a
// releasing title whose episode count rises (or arrives, having been unknown at
// add time) gains the missing wanted items on the next pass. It never touches
// existing rows, so a refresh cannot clobber in_library or re-grab anything.
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

// titlesPerPass bounds how much of the request budget one pass can spend. Title
// due a refresh sort never-fetched first, so a title whose cache row was lost
// is picked up ahead of routine re-checks.
const titlesPerPass = 5

// Service upserts wanted items from re-fetched title metadata.
type Service struct {
	store    *store.Store
	provider metadata.Provider
	log      *slog.Logger
}

// New builds a Service over the shared cached provider. Sharing matters twice:
// the adapter underneath carries the rate limiter, and the cache's fetched_at
// stamp is what marks a title as no longer due.
func New(st *store.Store, provider metadata.Provider, log *slog.Logger) *Service {
	return &Service{store: st, provider: provider, log: log}
}

// RefreshOnce re-fetches metadata for every title due one, and is what the job
// runner calls. One title' error never costs the rest their refresh.
func (s *Service) RefreshOnce(ctx context.Context) error {
	due, err := s.due(ctx)
	if err != nil {
		return fmt.Errorf("list series due a metadata refresh: %w", err)
	}

	var errs []error
	for _, title := range due {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := s.refreshTitle(ctx, title); err != nil {
			errs = append(errs, fmt.Errorf("series %d: %w", title.ID, err))
		}
	}
	return errors.Join(errs...)
}

// due lists the title whose title snapshot is missing or stale, pacing each
// status by the same TTL policy the title cache uses.
func (s *Service) due(ctx context.Context) ([]db.Series, error) {
	now := time.Now()
	cutoff := func(status string) string {
		return store.FormatTimestamp(now.Add(-metadata.TTLFor(status)))
	}
	return s.store.Q.ListTitlesDueMetadataRefresh(ctx, db.ListTitlesDueMetadataRefreshParams{
		Provider:    sql.NullString{String: s.provider.Name(), Valid: true},
		FetchedAt:   cutoff("FINISHED"),
		FetchedAt_2: cutoff("RELEASING"),
		Limit:       titlesPerPass,
	})
}

// refreshTitle upserts one title' items from a re-fetch, in one transaction.
// Clearing the airing stamp on growth is what gets the new items air dates: the
// next airing pass re-pages exactly the title that grew. Resetting the search
// cadence in the same transaction is the other half of that handshake — a new
// episode is worth looking for now, whatever backoff had accumulated.
func (s *Service) refreshTitle(ctx context.Context, title db.Series) error {
	meta, items, err := s.provider.GetTitle(ctx, title.ProviderID.Int64)
	if err != nil {
		return fmt.Errorf("fetch metadata: %w", err)
	}

	tx, err := s.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful Commit
	q := s.store.Q.WithTx(tx)

	// Counted apart (#188): the air-date sync ignores monitoring, so any growth
	// clears the stamp, but only a monitored insert is worth searching for.
	var inserted, monitoredInserts int64
	kind := domain.KindFor(domain.Format(title.Format))
	for _, it := range items {
		number := sql.NullInt64{Int64: int64(it.Number), Valid: true}
		monitored := store.MonitorNew(title.MonitorNewFrom, number)
		n, err := q.UpsertWantedItem(ctx, db.UpsertWantedItemParams{
			SeriesID:  title.ID,
			Kind:      string(kind),
			Number:    number,
			Title:     nullString(it.Name),
			Monitored: monitored,
		})
		if err != nil {
			return fmt.Errorf("upsert item %d: %w", it.Number, err)
		}
		inserted += n
		monitoredInserts += n * monitored
	}
	// Unconditional: the query is its own no-op, so a film added before AniList
	// published a date gains one on cadence and a null never erases one.
	if err := q.SetTitleYear(ctx, db.SetTitleYearParams{
		Year: int64(meta.Year), ID: title.ID, Column3: int64(meta.Year),
	}); err != nil {
		return fmt.Errorf("set the year: %w", err)
	}
	if inserted > 0 {
		if err := q.ClearTitleAiringSyncedAt(ctx, title.ID); err != nil {
			return fmt.Errorf("clear airing stamp: %w", err)
		}
	}
	if monitoredInserts > 0 {
		if err := q.ResetTitleSearchState(ctx, title.ID); err != nil {
			return fmt.Errorf("reset search cadence: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	if inserted > 0 {
		s.log.Info("series grew", "series", title.ID, "new_items", inserted)
	}
	return nil
}

func nullString(v string) sql.NullString {
	if v == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: v, Valid: true}
}
