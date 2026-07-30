package acquire

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/decide"
	"github.com/matthewdias/transpondarr/internal/core/domain"
	"github.com/matthewdias/transpondarr/internal/core/indexer"
	"github.com/matthewdias/transpondarr/internal/store"
	"github.com/matthewdias/transpondarr/internal/store/db"
)

// seriesPerPass bounds how much of the indexer budget one pass can spend. Due
// series sort never-searched first, so a newly added title is picked up on the
// next tick rather than queued behind a backlog.
const seriesPerPass = 5

// statusFailed is the one settled grab status that leaves an item wanted again.
const statusFailed = "failed"

// Empty-handed passes back off from an hour, doubling to a daily floor.
const (
	backoffBase = time.Hour
	backoffCap  = 24 * time.Hour
)

// backoffDelay is the wait after n consecutive empty searches.
func backoffDelay(n int) time.Duration {
	if n < 1 {
		return backoffBase
	}
	if n > 6 || backoffBase<<(n-1) > backoffCap {
		return backoffCap
	}
	return backoffBase << (n - 1)
}

// sweepItem is one wanted item with everything the pass needs to reason about it.
type sweepItem struct {
	id        int64
	kind      domain.WantedKind
	number    int
	had       bool
	airsAt    time.Time // zero when the provider published none
	grabbable bool
}

// SweepOnce searches every series due one and grabs what it can, and is what the
// job runner calls. Both the kill switch and the clients are read per run, so
// enabling automation or configuring an integration takes effect on the next
// tick without a restart. One series' failure never costs the rest their pass.
func (s *Service) SweepOnce(ctx context.Context) error {
	if !s.cfg.AutomationEnabled() {
		return nil
	}
	idx := s.clients.Indexer()
	if idx == nil || s.clients.Download() == nil {
		return nil
	}

	now := time.Now()
	stamp := sql.NullString{String: store.FormatTimestamp(now), Valid: true}
	due, err := s.store.Q.ListSeriesDueWantedSearch(ctx, db.ListSeriesDueWantedSearchParams{
		NextSearchAt: stamp,
		AirsAt:       stamp,
		Limit:        seriesPerPass,
	})
	if err != nil {
		return fmt.Errorf("list series due a wanted search: %w", err)
	}

	var errs []error
	for _, series := range due {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := s.sweepSeries(ctx, idx, series, now); err != nil {
			errs = append(errs, fmt.Errorf("series %d: %w", series.ID, err))
		}
	}
	return errors.Join(errs...)
}

// sweepSeries searches one series and grabs every eligible release covering
// items nothing else in this pass already took. A failure leaves the cadence
// untouched, so the series is retried on the next tick rather than backed off
// for a fault that was never its own.
func (s *Service) sweepSeries(ctx context.Context, idx indexer.Indexer, series db.Series, now time.Time) error {
	sweep, err := s.loadSweepItems(ctx, series.ID, now)
	if err != nil {
		return err
	}

	// Items not worth grabbing are handed to the matcher as Have: decide already
	// excludes had items from the wanted set while maxItem still spans them, so
	// in-flight suppression falls out of the existing matcher and a batch covering
	// an in-flight episode still matches the rest.
	items := make([]domain.WantedItem, 0, len(sweep))
	for _, it := range sweep {
		items = append(items, domain.WantedItem{
			ID: it.id, Kind: it.kind, Number: it.number, Have: !it.grabbable,
		})
	}

	m, err := s.match(ctx, idx, series, items)
	if err != nil {
		return err
	}

	grabbed, held, err := s.grabPass(ctx, series, m, sweep, now)
	if err != nil {
		return err
	}
	return s.writeSearchState(ctx, series, sweep, now, grabbed, held)
}

// grabPass walks the ranked candidates once, taking every eligible release whose
// items are still unspoken for. It returns how many releases were grabbed and
// the earliest moment a held release becomes grabbable (zero when none is held).
func (s *Service) grabPass(ctx context.Context, series db.Series, m Match, sweep []sweepItem, now time.Time) (int, time.Time, error) {
	airs := make(map[int]time.Time, len(sweep))
	for _, it := range sweep {
		if !it.airsAt.IsZero() {
			airs[it.number] = it.airsAt
		}
	}

	covered := make(map[int]bool, len(sweep))
	var grabbed int
	var held time.Time
	for _, c := range m.Candidates {
		// Eligibility is enforcement here, unlike a manual grab (PR #57).
		if !c.Matched || !c.Eligible || len(c.Items) == 0 || anyCovered(covered, c.Items) {
			continue
		}
		if until, ok := s.pinHold(series, c, airs, now); ok {
			if held.IsZero() || until.Before(held) {
				held = until
			}
			markCovered(covered, c.Items)
			continue
		}
		if _, err := s.Grab(ctx, c, m.Items, false); err != nil {
			return 0, time.Time{}, err
		}
		s.log.Info("sweep grabbed a release", "series", series.ID, "release", c.Release.Title, "items", c.Items)
		markCovered(covered, c.Items)
		grabbed++
	}
	return grabbed, held, nil
}

// writeSearchState records what the pass found. The write is guarded on the
// cadence read at selection, so a reset that landed mid-sweep wins.
func (s *Service) writeSearchState(ctx context.Context, series db.Series, sweep []sweepItem, now time.Time, grabbed int, held time.Time) error {
	backoff := series.SearchBackoff
	var next time.Time
	switch {
	case grabbed > 0:
		// Something landed, so more may be waiting: due again next tick.
		backoff = 0
	case !held.IsZero():
		// A held item is never backed off past its own window.
		backoff = 0
		next = earliest(held, nextAiring(sweep, now))
	default:
		if airedSince(sweep, series.LastSearchedAt, now) {
			backoff = 0
		}
		backoff++
		next = earliest(now.Add(backoffDelay(int(backoff))), nextAiring(sweep, now))
	}

	return s.store.Q.SetSeriesSearchState(ctx, db.SetSeriesSearchStateParams{
		ID:             series.ID,
		LastSearchedAt: sql.NullString{String: store.FormatTimestamp(now), Valid: true},
		SearchBackoff:  backoff,
		NextSearchAt:   nullTimestamp(next),
		NextSearchAt_2: series.NextSearchAt,
	})
}

// loadSweepItems reads every wanted item with the grab state that decides
// whether it is worth searching for right now.
func (s *Service) loadSweepItems(ctx context.Context, seriesID int64, now time.Time) ([]sweepItem, error) {
	rows, err := s.store.Q.ListWantedItemsWithGrabState(ctx, seriesID)
	if err != nil {
		return nil, fmt.Errorf("load wanted items with grab state: %w", err)
	}
	out := make([]sweepItem, 0, len(rows))
	for _, r := range rows {
		it := sweepItem{
			id:     r.ID,
			kind:   domain.WantedKind(r.Kind),
			number: int(r.Number.Int64),
			had:    r.Have == 1,
		}
		if r.AirsAt.Valid {
			if t, perr := store.ParseTimestamp(r.AirsAt.String); perr == nil {
				it.airsAt = t
			}
		}
		settled := r.GrabStatus.Valid && r.GrabStatus.String != statusFailed
		it.grabbable = !it.had && !settled && (it.airsAt.IsZero() || !it.airsAt.After(now))
		out = append(out, it)
	}
	return out, nil
}

// nextAiring is the earliest broadcast still ahead of us among items we do not
// have, or the zero time when nothing is scheduled.
func nextAiring(sweep []sweepItem, now time.Time) time.Time {
	var next time.Time
	for _, it := range sweep {
		if it.had || it.airsAt.IsZero() || !it.airsAt.After(now) {
			continue
		}
		if next.IsZero() || it.airsAt.Before(next) {
			next = it.airsAt
		}
	}
	return next
}

// airedSince reports whether an episode broadcast between the last search and
// now — #100's "a new episode resets the clock", read off items already loaded.
func airedSince(sweep []sweepItem, lastSearched sql.NullString, now time.Time) bool {
	var since time.Time
	if lastSearched.Valid {
		if t, err := store.ParseTimestamp(lastSearched.String); err == nil {
			since = t
		}
	}
	for _, it := range sweep {
		if it.had || it.airsAt.IsZero() || it.airsAt.After(now) {
			continue
		}
		if it.airsAt.After(since) {
			return true
		}
	}
	return false
}

func anyCovered(covered map[int]bool, items []int) bool {
	for _, n := range items {
		if covered[n] {
			return true
		}
	}
	return false
}

func markCovered(covered map[int]bool, items []int) {
	for _, n := range items {
		covered[n] = true
	}
}

// earliest picks the sooner of two instants, ignoring zero values.
func earliest(a, b time.Time) time.Time {
	switch {
	case a.IsZero():
		return b
	case b.IsZero():
		return a
	case b.Before(a):
		return b
	default:
		return a
	}
}

func nullTimestamp(t time.Time) sql.NullString {
	if t.IsZero() {
		return sql.NullString{}
	}
	return sql.NullString{String: store.FormatTimestamp(t), Valid: true}
}

// pinHold reports how long a candidate must wait for the series' pinned group.
// Implemented with the delay semantics (#62); see the package doc.
func (s *Service) pinHold(_ db.Series, _ decide.Candidate, _ map[int]time.Time, _ time.Time) (time.Time, bool) {
	return time.Time{}, false
}
