package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/matthewdias/transpondarr/internal/core/domain"
	"github.com/matthewdias/transpondarr/internal/store"
	"github.com/matthewdias/transpondarr/internal/store/db"
)

// ErrTitleHasItems refuses a count for a title that already has items: raising
// maxItem on a healthy one is the same hazard as inferring the count from a
// release name, only human-triggered.
var ErrTitleHasItems = errors.New("catalog: series already has wanted items")

// SetItemCount materializes items 1..count for a title that has none, which is
// the only thing that unsticks a title the provider published neither a count
// nor a schedule for. The count itself is not stored: refresh only ever adds, so
// there is nothing a later provider answer could clobber.
func (s *Service) SetItemCount(ctx context.Context, titleID int64, count int) (int64, error) {
	tx, err := s.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful Commit
	q := s.store.Q.WithTx(tx)

	title, err := q.GetTitle(ctx, titleID)
	if err != nil {
		return 0, fmt.Errorf("load series: %w", err)
	}
	// Read inside the transaction, so a concurrent add cannot slip items past it.
	existing, err := q.ListWantedItems(ctx, titleID)
	if err != nil {
		return 0, fmt.Errorf("list existing items: %w", err)
	}
	if len(existing) > 0 {
		return 0, ErrTitleHasItems
	}

	// Counted apart (#188), as refresh does it: the air-date sync ignores
	// monitoring, but only a monitored insert is worth searching for.
	var inserted, monitoredInserts int64
	kind := domain.KindFor(domain.Format(title.Format))
	for n := 1; n <= count; n++ {
		number := sql.NullInt64{Int64: int64(n), Valid: true}
		monitored := store.MonitorNew(title.MonitorNewFrom, number)
		created, err := q.UpsertWantedItem(ctx, db.UpsertWantedItemParams{
			SeriesID: titleID, Kind: string(kind), Number: number, Monitored: monitored,
		})
		if err != nil {
			return 0, fmt.Errorf("create item %d: %w", n, err)
		}
		inserted += created
		monitoredInserts += created * monitored
	}
	if inserted > 0 {
		if err := q.ClearTitleAiringSyncedAt(ctx, titleID); err != nil {
			return 0, fmt.Errorf("clear airing stamp: %w", err)
		}
	}
	if monitoredInserts > 0 {
		if err := q.ResetTitleSearchState(ctx, titleID); err != nil {
			return 0, fmt.Errorf("reset search cadence: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	return inserted, nil
}
