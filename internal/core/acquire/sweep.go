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
	"github.com/matthewdias/transpondarr/internal/core/jobs"
	"github.com/matthewdias/transpondarr/internal/store"
	"github.com/matthewdias/transpondarr/internal/store/db"
)

// seriesPerPass bounds how much of the indexer budget one pass can spend. Due
// series sort never-searched first, so a newly added title is picked up on the
// next tick rather than queued behind a backlog.
const seriesPerPass = 5

// statusFailed is the one settled grab status that leaves an item wanted again.
const statusFailed = "failed"

// maxAddFailures ends a series' pass once the download client has refused this
// many releases: past a couple, the client is unwell rather than the releases.
const maxAddFailures = 3

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
// job runner calls. The clients and the kill switch are both read per run, so
// configuring an integration or flipping automation in Settings takes effect on
// the next tick without a restart — except on a hand-triggered run, which passes
// the kill switch as explicit intent (#122). One series' failure never costs the
// rest their pass.
func (s *Service) SweepOnce(ctx context.Context) error {
	// Gated jobs are mirrored in the UI's AUTOMATION_GATED list (jobs.tsx).
	if !s.cfg.AutomationEnabled() && !jobs.ManualRun(ctx) {
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

	// Read once per pass, from the indexer this run resolved, so a Settings edit
	// that adds or removes a feed applies on the next tick.
	_, hasFeed := idx.(indexer.RecentFeed)

	var errs []error
	for _, series := range due {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := s.sweepSeries(ctx, idx, series, now, hasFeed); err != nil {
			errs = append(errs, fmt.Errorf("series %d: %w", series.ID, err))
		}
	}
	return errors.Join(errs...)
}

// sweepSeries searches one series and grabs every eligible release covering
// items nothing else in this pass already took.
func (s *Service) sweepSeries(ctx context.Context, idx indexer.Indexer, series db.Series, now time.Time, hasFeed bool) error {
	sweep, err := s.loadSweepItems(ctx, series.ID, now)
	if err != nil {
		return errors.Join(err, s.backOffAfterFailure(ctx, series, now))
	}

	m, err := s.match(ctx, idx, series, grabbableItems(sweep))
	if err != nil {
		// An indexer outage is the one fault a series is not charged for: every due
		// series shares it, so backing them all off idles the library on one hiccup.
		if errors.Is(err, ErrIndexerSearch) {
			return err
		}
		return errors.Join(err, s.backOffAfterFailure(ctx, series, now))
	}

	grabbed, held, err := s.grabPass(ctx, series, m, sweep, now, "sweep")
	// A pass that landed something is progress even if it ended badly, and its
	// successful grabs settle those items, so it records the ordinary cadence.
	if err != nil && grabbed == 0 {
		return errors.Join(err, s.backOffAfterFailure(ctx, series, now))
	}
	return errors.Join(err, s.writeSearchState(ctx, series, sweep, now, grabbed, held, hasFeed))
}

// backOffAfterFailure makes a failed pass yield its slot. The due query is a
// small LIMIT ordered by next_search_at, so a series that keeps failing without
// this holds the head of the queue and starves every healthy series behind it.
// last_searched_at deliberately stays put: nothing was searched, and moving it
// would hide an already-aired episode from airedSince.
func (s *Service) backOffAfterFailure(ctx context.Context, series db.Series, now time.Time) error {
	if ctx.Err() != nil {
		return nil
	}
	backoff := series.SearchBackoff + 1
	return s.setSearchState(ctx, db.SetSeriesSearchStateParams{
		ID:             series.ID,
		LastSearchedAt: series.LastSearchedAt,
		SearchBackoff:  backoff,
		NextSearchAt:   nullTimestamp(now.Add(backoffDelay(int(backoff)))),
		SearchEpoch:    series.SearchEpoch,
	})
}

// grabbableItems is the item list the matcher gets. Items not worth grabbing are
// handed over as Have: decide already excludes had items from the wanted set
// while maxItem still spans them, so in-flight suppression falls out of the
// existing matcher and a batch covering an in-flight episode still matches the rest.
func grabbableItems(sweep []sweepItem) []domain.WantedItem {
	items := make([]domain.WantedItem, 0, len(sweep))
	for _, it := range sweep {
		items = append(items, domain.WantedItem{
			ID: it.id, Kind: it.kind, Number: it.number, Have: !it.grabbable,
		})
	}
	return items
}

// grabPass walks the ranked candidates once, taking every eligible release whose
// items are still unspoken for. It returns how many releases were grabbed and
// the earliest moment a held release becomes grabbable (zero when none is held).
// source names the entry point that drove it — the sweep or the feed poll — so a
// log line says which of the two acted.
func (s *Service) grabPass(ctx context.Context, series db.Series, m Match, sweep []sweepItem, now time.Time, source string) (int, time.Time, error) {
	airs := make(map[int]time.Time, len(sweep))
	for _, it := range sweep {
		if !it.airsAt.IsZero() {
			airs[it.number] = it.airsAt
		}
	}

	covered := make(map[int]bool, len(sweep))
	var grabbed, failed int
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
			// Marking the whole release covered holds items whose own window has
			// long closed, when a batch's anchor is its newest episode. Deliberate:
			// taking an old episode separately and the batch later is worse.
			markCovered(covered, c.Items)
			continue
		}
		if _, err := s.AutoGrab(ctx, series.ID, c, m.Items); err != nil {
			// Another grab has these items — in flight, or settled since this pass read
			// them. Leave them uncovered either way: an in-flight holder may still
			// fail, and a later pass must be free to retry them.
			if errors.Is(err, errItemsTaken) {
				continue
			}
			if !errors.Is(err, ErrDownloadAdd) {
				return grabbed, time.Time{}, err
			}
			// A dead download URL is this release's problem, not the series': the
			// items stay unclaimed so the next-ranked release is still tried, and
			// only a client that keeps refusing ends the pass. AutoGrab has already
			// remembered it if the release itself was at fault (#120).
			failed++
			s.log.Warn("could not add a release; trying the next candidate",
				"source", source, "series", series.ID, "release", c.Release.Title, "err", err)
			if failed >= maxAddFailures {
				return grabbed, time.Time{}, fmt.Errorf("%d refused adds: %w", failed, err)
			}
			continue
		}
		s.log.Info("grabbed a release",
			"source", source, "series", series.ID, "release", c.Release.Title, "items", c.Items)
		markCovered(covered, c.Items)
		grabbed++
	}
	return grabbed, held, nil
}

// writeSearchState records what the pass found. The write is guarded on the
// cadence read at selection, so a reset that landed mid-sweep wins.
//
// The airing-aimed parts of the cadence — the aired-since reset and the
// next-broadcast clamp — exist only for the feedless world. They are #100's
// answer to "a weekly show must be searched at air time", written when the sweep
// was the only mechanism; with a feed they aim the sweep at precisely the
// windows the feed already covers, for one search per series. A series whose
// broadcast the feed then misses is still reached: its stale next_search_at plus
// the due query's EXISTS make it due, and the backoff ladder caps at a day.
func (s *Service) writeSearchState(ctx context.Context, series db.Series, sweep []sweepItem, now time.Time, grabbed int, held time.Time, hasFeed bool) error {
	upcoming := nextAiring(sweep, now)
	if hasFeed {
		upcoming = time.Time{}
	}

	backoff := series.SearchBackoff
	var next time.Time
	switch {
	case grabbed > 0:
		// Something landed, so more may be waiting: due again next tick.
		backoff = 0
	case !held.IsZero():
		// A held item is never backed off past its own window. The pin delay stays
		// the sweep's business either way: the release already exists, so no feed
		// poll will produce it sooner.
		backoff = 0
		next = earliest(held, upcoming)
	default:
		if !hasFeed && airedSince(sweep, series.LastSearchedAt, now) {
			backoff = 0
		}
		backoff++
		next = earliest(now.Add(backoffDelay(int(backoff))), upcoming)
	}

	return s.setSearchState(ctx, db.SetSeriesSearchStateParams{
		ID:             series.ID,
		LastSearchedAt: sql.NullString{String: store.FormatTimestamp(now), Valid: true},
		SearchBackoff:  backoff,
		NextSearchAt:   nullTimestamp(next),
		SearchEpoch:    series.SearchEpoch,
	})
}

// setSearchState applies a cadence write and reports a lost epoch guard rather
// than swallowing it: zero rows means a reset landed mid-sweep and deliberately
// won, or the series is gone. Neither is an error, but both explain a backoff
// that silently did not stick.
func (s *Service) setSearchState(ctx context.Context, p db.SetSeriesSearchStateParams) error {
	rows, err := s.store.Q.SetSeriesSearchState(ctx, p)
	if err != nil {
		return fmt.Errorf("write search cadence for series %d: %w", p.ID, err)
	}
	if rows == 0 {
		s.log.Debug("search cadence write skipped; the series was reset or removed mid-sweep",
			"series", p.ID, "epoch", p.SearchEpoch)
	}
	return nil
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

// pinHold reports when a candidate becomes grabbable, and whether it must wait
// at all (#62). Only another group's release ever waits, and only while the
// window since the latest covered broadcast is still open — a covered item with
// no air date makes that window unmeasurable, so the delay does not apply rather
// than anchoring to now, which would restart the wait on every process restart.
func (s *Service) pinHold(series db.Series, c decide.Candidate, airs map[int]time.Time, now time.Time) (time.Time, bool) {
	if !series.PinnedGroup.Valid || series.PinnedGroup.String == "" || c.Pinned {
		return time.Time{}, false
	}
	delay := s.cfg.PinDelayDefault()
	if series.PinDelayHours.Valid {
		delay = domain.PinDelay(series.PinDelayHours.Int64)
	}
	if delay <= 0 {
		return time.Time{}, false
	}

	var anchor time.Time
	for _, n := range c.Items {
		at, ok := airs[n]
		if !ok {
			return time.Time{}, false
		}
		if at.After(anchor) {
			anchor = at
		}
	}
	if until := anchor.Add(delay); until.After(now) {
		return until, true
	}
	return time.Time{}, false
}
