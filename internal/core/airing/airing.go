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

// titlesPerPass bounds how much of the request budget one pass can spend. Title
// due for a sync sort never-synced first, so a newly added title is picked up on
// the next tick rather than queued behind a backlog of routine refreshes.
const titlesPerPass = 5

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

// SyncOnce fetches schedules for every title due one, and is what the job runner
// calls. A provider that publishes no schedule is a supported configuration, not
// a failure. One title's error never costs the rest their sync.
func (s *Service) SyncOnce(ctx context.Context) error {
	airing, ok := s.provider.(metadata.AiringProvider)
	if !ok {
		return nil
	}

	due, err := s.due(ctx)
	if err != nil {
		return fmt.Errorf("list series due an airing sync: %w", err)
	}

	// A provider outage fails every due title with one cause, so causes are
	// collapsed to a count: N copies of the same blob is not N failures.
	var failures []*syncFailure
	seen := map[string]*syncFailure{}
	for _, title := range due {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		err := s.syncTitle(ctx, airing, title)
		if err == nil {
			continue
		}
		if f, ok := seen[err.Error()]; ok {
			f.titles++
			continue
		}
		f := &syncFailure{first: title.ID, err: err, titles: 1}
		seen[err.Error()] = f
		failures = append(failures, f)
	}

	errs := make([]error, 0, len(failures))
	for _, f := range failures {
		errs = append(errs, f.summary())
	}
	return errors.Join(errs...)
}

// syncFailure is one cause and how many titles hit it.
type syncFailure struct {
	first  int64
	err    error
	titles int
}

func (f *syncFailure) summary() error {
	if f.titles == 1 {
		return fmt.Errorf("series %d: %w", f.first, f.err)
	}
	return fmt.Errorf("%d series including %d: %w", f.titles, f.first, f.err)
}

// due lists the title whose schedule has never been synced or has gone stale,
// pacing each status by the same TTL policy the title cache uses.
func (s *Service) due(ctx context.Context) ([]db.Series, error) {
	now := time.Now()
	// Always countKnown: aired times are immutable, so this query's CASE keys on
	// status alone and the unknown-count tier (#151) has nothing to say here.
	cutoff := func(status string) sql.NullString {
		return sql.NullString{String: store.FormatTimestamp(now.Add(-metadata.TTLFor(status, true))), Valid: true}
	}
	return s.store.Q.ListTitlesDueAiringSync(ctx, db.ListTitlesDueAiringSyncParams{
		Provider:         sql.NullString{String: s.provider.Name(), Valid: true},
		AiringSyncedAt:   cutoff("FINISHED"),
		AiringSyncedAt_2: cutoff("RELEASING"),
		Limit:            titlesPerPass,
	})
}

// syncTitle writes one title's schedule, then stamps it as synced. The stamp is
// what stops a title AniList has no schedule for (its coverage thins out badly
// before ~2015) from being re-asked every tick forever.
func (s *Service) syncTitle(ctx context.Context, airing metadata.AiringProvider, title db.Series) error {
	// A title synced before has its aired history already; only the not-yet-aired
	// tail can still move, and that is 1-2 pages instead of one per 50 episodes.
	notYetAired := title.AiringSyncedAt.Valid

	schedule, err := airing.GetSchedule(ctx, title.ProviderID.Int64, notYetAired)
	if err != nil {
		return fmt.Errorf("fetch schedule: %w", err)
	}

	kind := domain.KindFor(domain.Format(title.Format))
	var premiere time.Time
	if kind == domain.KindMovie {
		schedule = premiereOnly(schedule)
		if len(schedule) == 0 {
			// Fetched here, before the transaction, for the same reason the schedule
			// is: no write lock may be held across the network.
			meta, _, err := s.provider.GetTitle(ctx, title.ProviderID.Int64)
			if err != nil {
				return fmt.Errorf("fetch title for a release date: %w", err)
			}
			premiere = meta.Premiere
		}
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
		number := sql.NullInt64{Int64: int64(a.Number), Valid: true}
		if err := q.UpsertWantedItemAiring(ctx, db.UpsertWantedItemAiringParams{
			SeriesID:  title.ID,
			Kind:      string(kind),
			Number:    number,
			AirsAt:    sql.NullString{String: store.FormatTimestamp(a.AirsAt), Valid: true},
			Monitored: store.MonitorNew(title.MonitorNewFrom, number),
		}); err != nil {
			return fmt.Errorf("upsert airing for item %d: %w", a.Number, err)
		}
	}

	// A film that never broadcasts has only its release date, which names a day
	// rather than a moment — so it fills a gap and never displaces a real node.
	if !premiere.IsZero() {
		if err := q.SetWantedItemAirsAtIfNull(ctx, db.SetWantedItemAirsAtIfNullParams{
			AirsAt:   sql.NullString{String: store.FormatTimestamp(premiere), Valid: true},
			SeriesID: title.ID,
			Kind:     string(kind),
			Number:   sql.NullInt64{Int64: 1, Valid: true},
		}); err != nil {
			return fmt.Errorf("date the film from its release date: %w", err)
		}
	}

	// Only monitored fills count: for a narrowed long-runner the fill range is
	// the back catalogue, and nothing will grab it.
	var filled int64
	for _, n := range skipped(schedule, !notYetAired) {
		number := sql.NullInt64{Int64: int64(n), Valid: true}
		monitored := store.MonitorNew(title.MonitorNewFrom, number)
		rows, err := q.UpsertWantedItem(ctx, db.UpsertWantedItemParams{
			SeriesID:  title.ID,
			Kind:      string(kind),
			Number:    number,
			Monitored: monitored,
		})
		if err != nil {
			return fmt.Errorf("create item %d for a skipped schedule entry: %w", n, err)
		}
		filled += rows * monitored
	}
	// A filled item has no air date, so it is exactly what airedSince cannot see.
	if filled > 0 {
		if err := q.ResetTitleSearchState(ctx, title.ID); err != nil {
			return fmt.Errorf("reset search cadence: %w", err)
		}
	}

	// Guarded on the stamp read at selection: if the metadata refresh cleared it
	// mid-sync (the title grew), the clear wins and the next pass re-pages.
	if err := q.SetTitleAiringSyncedAt(ctx, db.SetTitleAiringSyncedAtParams{
		ID:             title.ID,
		AiringSyncedAt: title.AiringSyncedAt,
	}); err != nil {
		return fmt.Errorf("stamp synced: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	s.log.Debug("airing schedule synced", "series", title.ID, "airings", len(schedule), "tail_only", notYetAired)
	return nil
}

// premiereOnly clamps a film's schedule to its single item: AniList publishes
// nodes for films with a TV premiere, and one numbered past 1 would create items
// 2..N. Collapsing to one node makes the gap fill below a no-op by construction.
func premiereOnly(schedule []metadata.Airing) []metadata.Airing {
	for _, a := range schedule {
		if a.Number == 1 {
			return []metadata.Airing{a}
		}
	}
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
