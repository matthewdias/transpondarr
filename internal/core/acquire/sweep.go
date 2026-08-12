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
	"github.com/matthewdias/transpondarr/internal/core/notify"
	"github.com/matthewdias/transpondarr/internal/store"
	"github.com/matthewdias/transpondarr/internal/store/db"
)

// seriesPerPass bounds how much of the indexer budget one pass can spend. Due
// series sort never-searched first, so a newly added title is picked up on the
// next tick rather than queued behind a backlog.
const seriesPerPass = 5

// Settled grab statuses the pass reasons about: failed leaves an item wanted
// again, imported is what an upgrade re-opens.
const (
	statusFailed   = "failed"
	statusImported = "imported"
)

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

// passSource names the entry point driving a pass. It is control flow, not just
// a log field: only the sweep spent a search on this series, so only the sweep
// reports a rehearsal that would have done nothing.
type passSource string

const (
	sourceSweep passSource = "sweep"
	sourceFeed  passSource = "feed"
)

// sweepItem is one wanted item with everything the pass needs to reason about
// it. monitored stays beside grabbable: the cadence helpers below need it on
// items grabbable can never describe.
type sweepItem struct {
	id        int64
	kind      domain.WantedKind
	number    int
	inLibrary bool
	airsAt    time.Time // zero when the provider published none
	monitored bool
	grabbable bool
	heldTitle string // what the library holds, when this item is in the upgrade pool
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

	m, err := s.match(ctx, idx, series, passItems(sweep))
	if err != nil {
		// An indexer outage is the one fault a series is not charged for: every due
		// series shares it, so backing them all off idles the library on one hiccup.
		if errors.Is(err, ErrIndexerSearch) {
			return err
		}
		return errors.Join(err, s.backOffAfterFailure(ctx, series, now))
	}

	grabbed, held, err := s.grabPass(ctx, series, m, sweep, now, sourceSweep)
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

// passItems is the item list the matcher gets. In-flight and unaired items are
// handed over as non-candidates while InLibrary keeps reporting the library
// alone: decide excludes non-candidates from the wanted set while maxItem spans
// them, so in-flight suppression falls out of the existing matcher and a batch
// covering an in-flight episode still matches the rest.
func passItems(sweep []sweepItem) []passItem {
	items := make([]passItem, 0, len(sweep))
	for _, it := range sweep {
		items = append(items, passItem{
			WantedItem: domain.WantedItem{ID: it.id, Kind: it.kind, Number: it.number, InLibrary: it.inLibrary},
			grabbable:  it.grabbable,
			heldTitle:  it.heldTitle,
		})
	}
	return items
}

// grabPass walks the ranked candidates once, taking every eligible release whose
// items are still unspoken for. It returns how many releases were grabbed and
// the earliest moment a held release becomes grabbable (zero when none is held).
// source names the entry point that drove it — the sweep or the feed poll — so a
// log line says which of the two acted.
//
// In notify-only (#116) the walk is the same walk — the one decision layer both
// entry points share — but every take dispatches a rehearsal event instead of
// grabbing, and the count returned is 0. So the search cadence is rehearsed and
// the grab-driven reset is not: a real grab makes a series due next tick, while
// a would-grab backs it off, because nothing settled and counting it would
// re-decide the same items every tick. Switching to on clears that (see
// settings.UpdateAutomation).
func (s *Service) grabPass(ctx context.Context, series db.Series, m Match, sweep []sweepItem, now time.Time, source passSource) (int, time.Time, error) {
	res, err := s.walkCandidates(ctx, series, m, sweep, now, source)
	idx := indexCandidates(m.Candidates)
	// A pass that gave up partway still decided something real, so what it did
	// decide is flushed on the error path too (#181).
	finalizeOutcomes(&res, idx, sweep, source)
	s.persistOutcomes(ctx, sweep, res.outcomes, source, now)
	if err != nil {
		return res.grabbed, time.Time{}, err
	}
	if res.rehearsed {
		// "Would have done nothing, and here's why" is the useful half of a
		// rehearsal — but only the sweep's, which spent a search on this series;
		// per-series silence is the feed page's normal state.
		if source == sourceSweep {
			s.rehearseNoAction(ctx, series, idx, sweep, res.covered)
		}
		return 0, res.held, nil
	}
	return res.grabbed, res.held, nil
}

// walkResult is what one walk of the ranked candidates decided. covered and
// outcomes are kept side by side rather than merged: covered runs per candidate
// on the feed's hot path. They agree by invariant — an item is covered exactly
// when a settling outcome closed it (see walk_invariant_test.go).
type walkResult struct {
	grabbed   int
	held      time.Time
	covered   map[int]bool
	outcomes  outcomeSet
	rehearsed bool // notify-only was on, so nothing reached the download client
	complete  bool // false when the walk returned early and never saw the rest
}

// walkCandidates takes every eligible release whose items are still unspoken
// for, in rank order. It is split from grabPass so there is one exit that acts
// on what the walk decided.
func (s *Service) walkCandidates(ctx context.Context, series db.Series, m Match, sweep []sweepItem, now time.Time, source passSource) (walkResult, error) {
	notifyOnly := s.cfg.NotifyOnly()
	airs := make(map[int]time.Time, len(sweep))
	for _, it := range sweep {
		if !it.airsAt.IsZero() {
			airs[it.number] = it.airsAt
		}
	}

	covered := make(map[int]bool, len(sweep))
	res := walkResult{covered: covered, outcomes: outcomeSet{}, rehearsed: notifyOnly}
	var failed int
	for _, c := range m.Candidates {
		// Eligibility is enforcement here, unlike a manual grab (PR #57). The take
		// set is Items minus the held items the upgrade policy refused; a blocked
		// item is deliberately left uncovered, so a lower-ranked release that does
		// qualify -- its own group's v2 -- is still reached this pass.
		take := c.TakeItems()
		if !c.Matched || !c.Eligible || len(take) == 0 {
			continue
		}
		// Records nothing: anyCovered is true on a single overlapping item, so the
		// rest of take was not contended, and claiming it here would bury their
		// own refusal under a tentative that outranks it. The fill answers them.
		if anyCovered(covered, take) {
			continue
		}
		if until, ok := s.pinHold(series, c, take, airs, now); ok {
			if res.held.IsZero() || until.Before(res.held) {
				res.held = until
			}
			// Marking the whole release covered holds items whose own window has
			// long closed, when a batch's anchor is its newest episode. Deliberate:
			// taking an old episode separately and the batch later is worse.
			markCovered(covered, take)
			res.outcomes.settle(take, outcome{
				kind:      OutcomePinHeld,
				release:   c.Release.Title,
				detail:    fmt.Sprintf("waiting for the pinned group %q", series.PinnedGroup.String),
				heldUntil: until,
			})
			if notifyOnly {
				s.dispatchRehearsal(ctx, series, take, c.Release.Title,
					fmt.Sprintf("would have waited: held %s for the pinned group %q",
						until.Sub(now).Round(time.Minute), series.PinnedGroup.String))
			}
			continue
		}
		if notifyOnly {
			s.log.Info("rehearsal: would have grabbed a release",
				"source", string(source), "series", series.ID, "release", c.Release.Title, "items", take)
			s.dispatchRehearsal(ctx, series, take, c.Release.Title, "would have grabbed")
			markCovered(covered, take)
			res.outcomes.settle(take, outcome{kind: OutcomeWouldGrab, release: c.Release.Title})
			res.grabbed++
			continue
		}
		if _, err := s.AutoGrab(ctx, series.ID, c, m.Items); err != nil {
			// Another grab has these items — in flight, or settled since this pass read
			// them. Leave them uncovered either way: an in-flight holder may still
			// fail, and a later pass must be free to retry them.
			if errors.Is(err, errItemsTaken) {
				res.outcomes.tentative(take, outcome{kind: OutcomeContended, release: c.Release.Title})
				continue
			}
			if !errors.Is(err, ErrDownloadAdd) {
				return res, err
			}
			// A dead download URL is this release's problem, not the series': the
			// items stay unclaimed so the next-ranked release is still tried, and
			// only a client that keeps refusing ends the pass. AutoGrab has already
			// remembered it if the release itself was at fault (#120).
			failed++
			res.outcomes.tentative(take, outcome{
				kind: OutcomeAddFailed, release: c.Release.Title, detail: err.Error(),
			})
			s.log.Warn("could not add a release; trying the next candidate",
				"source", string(source), "series", series.ID, "release", c.Release.Title, "err", err)
			if failed >= maxAddFailures {
				return res, fmt.Errorf("%d refused adds: %w", failed, err)
			}
			continue
		}
		s.log.Info("grabbed a release",
			"source", string(source), "series", series.ID, "release", c.Release.Title, "items", take)
		if d := s.clients.Notify(); d != nil {
			item := 0
			if len(take) == 1 {
				item = take[0]
			}
			d.Dispatch(ctx, notify.Event{
				Kind:         notify.KindGrabbed,
				Title:        series.Title,
				ItemNumber:   item,
				ReleaseTitle: c.Release.Title,
			})
		}
		markCovered(covered, take)
		res.outcomes.settle(take, outcome{kind: OutcomeGrabbed, release: c.Release.Title})
		res.grabbed++
	}
	res.complete = true
	return res, nil
}

// finalizeOutcomes fills in what the walk left unsaid: an uncovered item blames
// the refused candidate that came closest to covering it, and failing that the
// pass says nothing matched — but only a sweep that ran to the end may. A hard
// return never examined the remaining candidates, and a feed poll saw one page
// covering the whole library rather than a search for this series, so either
// claiming nothing matched would clobber a real refusal with a guess.
func finalizeOutcomes(res *walkResult, idx passIndex, sweep []sweepItem, source passSource) {
	for _, it := range sweep {
		if !it.grabbable || it.inLibrary || res.covered[it.number] {
			continue
		}
		release, reason := idx.bestRefusal([]int{it.number})
		switch {
		case release != "" && reason != "":
			res.outcomes.tentative([]int{it.number},
				outcome{kind: OutcomeDeclined, release: release, detail: reason})
		case idx.eligible[it.number]:
			// An eligible release covers it and the pass took an overlapping one
			// first, so it is next pass's business rather than anything to report.
			res.outcomes.tentative([]int{it.number}, outcome{kind: OutcomeDeferred})
		case res.complete && source == sourceSweep:
			res.outcomes.tentative([]int{it.number}, outcome{kind: OutcomeNoMatch})
		}
	}
}

// persistOutcomes writes one row per decided item. A failure only logs:
// sweepSeries treats a returned error with nothing grabbed as grounds to back
// the series off, so surfacing a failed display-column write would cost it its
// place in the search queue.
func (s *Service) persistOutcomes(ctx context.Context, sweep []sweepItem, set outcomeSet, source passSource, now time.Time) {
	if len(set) == 0 {
		return
	}
	stamp := store.FormatTimestamp(now)
	rows := make([]db.UpsertPassOutcomeParams, 0, len(set))
	for _, it := range sweep {
		// The upgrade pool is grabbable and in the library at once (#97), and those
		// rows can never be read back by the Missing listing. Number 0 is a NULL:
		// episode numbering is 1-based, so two numberless items would collapse
		// onto one row, exactly as they already do in covered.
		if it.number == 0 || !it.grabbable || it.inLibrary {
			continue
		}
		o, ok := set[it.number]
		if !ok {
			continue
		}
		rows = append(rows, db.UpsertPassOutcomeParams{
			WantedItemID: it.id,
			Outcome:      o.kind,
			Source:       string(source),
			ReleaseTitle: o.release,
			Detail:       o.detail,
			HeldUntil:    nullTimestamp(o.heldUntil),
			RecordedAt:   stamp,
		})
	}
	if len(rows) == 0 {
		return
	}

	tx, err := s.store.DB.BeginTx(ctx, nil)
	if err != nil {
		s.log.Warn("could not record what the pass decided", "err", err)
		return
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful Commit
	q := s.store.Q.WithTx(tx)
	for _, row := range rows {
		if err := q.UpsertPassOutcome(ctx, row); err != nil {
			s.log.Warn("could not record what the pass decided",
				"item", row.WantedItemID, "err", err)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		s.log.Warn("could not record what the pass decided", "err", err)
	}
}

// dispatchRehearsal reports one rehearsed decision (#116). The outcome is always
// spelled out: an adapter renders this field as the event's detail, so a correct
// "would have grabbed" must not arrive as a blank where a reason belongs.
func (s *Service) dispatchRehearsal(ctx context.Context, series db.Series, items []int, release, outcome string) {
	d := s.clients.Notify()
	if d == nil {
		return
	}
	item := 0
	if len(items) == 1 {
		item = items[0]
	}
	d.Dispatch(ctx, notify.Event{
		Kind:         notify.KindRehearsal,
		Title:        series.Title,
		ItemNumber:   item,
		ReleaseTitle: release,
		Error:        outcome,
	})
}

// rehearseNoAction reports the wanted items a searched pass would have left
// untouched, blaming the best matched-but-refused candidate when there is one.
// It reports on what the walk did not cover rather than on "nothing happened",
// so a series whose episode 1 was pin-held still says that 2 and 3 went
// unmatched — the mismatch a rehearsal exists to surface.
func (s *Service) rehearseNoAction(ctx context.Context, series db.Series, idx passIndex, sweep []sweepItem, covered map[int]bool) {
	var wanted []int
	for _, it := range sweep {
		if it.grabbable && !covered[it.number] {
			wanted = append(wanted, it.number)
		}
	}
	if len(wanted) == 0 {
		return
	}
	// The same selection the stored rows use, so the notification and the column
	// cannot disagree about which release came closest (#181).
	release, reason := idx.bestRefusal(wanted)
	if reason == "" {
		reason = "no matching release found"
	}
	s.dispatchRehearsal(ctx, series, wanted, release, "would have grabbed nothing: "+reason)
}

// writeSearchState records what the pass found. The write is guarded on the
// cadence read at selection, so a reset that landed mid-sweep wins.
// The airing-aimed parts of the cadence exist only for the feedless world; with
// a feed, a missed broadcast is the feed poll's gap reset to recover (#100, #140).
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
			id:        r.ID,
			kind:      domain.WantedKind(r.Kind),
			number:    int(r.Number.Int64),
			inLibrary: r.InLibrary == 1,
			monitored: r.Monitored == 1,
		}
		if r.AirsAt.Valid {
			if t, perr := store.ParseTimestamp(r.AirsAt.String); perr == nil {
				it.airsAt = t
			}
		}
		settled := r.GrabStatus.Valid && r.GrabStatus.String != statusFailed
		// The upgrade pool, mirroring the feed's due predicate: a held item whose
		// release is known and whose grab is settled either way an upgrade can
		// re-open. An unextracted deferral and an in-flight grab stay out.
		pool := it.inLibrary && r.HeldReleaseTitle != "" && r.GrabStatus.Valid &&
			(r.GrabStatus.String == statusImported || r.GrabStatus.String == statusFailed)
		if pool {
			it.heldTitle = r.HeldReleaseTitle
		}
		// The one expression both entry points read: sweep search, feed grab and
		// the upgrade pool at once.
		it.grabbable = it.monitored &&
			(pool || (!it.inLibrary && !settled && (it.airsAt.IsZero() || !it.airsAt.After(now))))
		out = append(out, it)
	}
	return out, nil
}

// nextAiring is the earliest broadcast still ahead of us among monitored items
// the library does not hold, or the zero time when nothing is scheduled.
// Grabbable would be empty here by construction: an unaired item is never one.
func nextAiring(sweep []sweepItem, now time.Time) time.Time {
	var next time.Time
	for _, it := range sweep {
		if !it.monitored || it.inLibrary || it.airsAt.IsZero() || !it.airsAt.After(now) {
			continue
		}
		if next.IsZero() || it.airsAt.Before(next) {
			next = it.airsAt
		}
	}
	return next
}

// airedSince reports whether a monitored episode broadcast between the last
// search and now — #100's "a new episode resets the clock", read off items
// already loaded.
func airedSince(sweep []sweepItem, lastSearched sql.NullString, now time.Time) bool {
	var since time.Time
	if lastSearched.Valid {
		if t, err := store.ParseTimestamp(lastSearched.String); err == nil {
			since = t
		}
	}
	for _, it := range sweep {
		if !it.monitored || it.inLibrary || it.airsAt.IsZero() || it.airsAt.After(now) {
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
func (s *Service) pinHold(series db.Series, c decide.Candidate, items []int, airs map[int]time.Time, now time.Time) (time.Time, bool) {
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
	for _, n := range items {
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
