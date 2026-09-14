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

// titlesPerPass bounds how many indexer searches one pass can make. Due title
// sort never-searched first, so a newly added title is searched on the next tick
// rather than queued behind a backlog.
const titlesPerPass = 5

// Settled grab statuses the pass acts on: failed makes an item wanted again,
// imported is what an upgrade re-opens.
const (
	statusFailed   = "failed"
	statusImported = "imported"
)

// maxAddFailures ends a title's pass once the download client has failed this
// many adds: past a couple, the fault is the client rather than the releases.
const maxAddFailures = 3

// A pass that grabbed nothing backs off from an hour, doubling to a daily cap.
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

// passSource names the entry point driving a pass. It is control flow, not only
// a log field: only the sweep searched for this title, so only the sweep
// reports a rehearsal that would have done nothing.
type passSource string

const (
	sourceSweep passSource = "sweep"
	sourceFeed  passSource = "feed"
)

// sweepItem is one wanted item with every field the pass decides from.
// monitored stays beside grabbable: the cadence helpers below need it on items
// grabbable can never describe.
type sweepItem struct {
	id        int64
	kind      domain.WantedKind
	number    int
	inLibrary bool
	airsAt    time.Time // zero when the provider published none
	monitored bool
	grabbable bool
	heldTitle string // the release name in the library, for an item in the upgrade pool
}

// SweepOnce searches every title due one and grabs what it can, and is what the
// job runner calls. The clients and the kill switch are both read per run, so
// configuring an integration or flipping automation in Settings takes effect on
// the next tick without a restart — except on a manually triggered run, which
// passes the kill switch as explicit intent (#122). One title's failure never
// stops the rest of the pass.
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
	due, err := s.store.Q.ListTitlesDueWantedSearch(ctx, db.ListTitlesDueWantedSearchParams{
		NextSearchAt: stamp,
		AirsAt:       stamp,
		Limit:        titlesPerPass,
	})
	if err != nil {
		return fmt.Errorf("list titles due a wanted search: %w", err)
	}

	// Read once per pass, from the indexer this run resolved, so a Settings edit
	// that adds or removes a feed applies on the next tick.
	_, hasFeed := idx.(indexer.RecentFeed)

	var errs []error
	for _, title := range due {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := s.sweepTitle(ctx, idx, title, now, hasFeed); err != nil {
			errs = append(errs, fmt.Errorf("title %d: %w", title.ID, err))
		}
	}
	return errors.Join(errs...)
}

// sweepTitle searches one title and grabs every eligible release covering
// items nothing else in this pass already grabbed.
func (s *Service) sweepTitle(ctx context.Context, idx indexer.Indexer, title db.Series, now time.Time, hasFeed bool) error {
	sweep, err := s.loadSweepItems(ctx, title.ID, now)
	if err != nil {
		return errors.Join(err, s.backOffAfterFailure(ctx, title, now))
	}

	m, err := s.match(ctx, idx, title, passItems(sweep))
	if err != nil {
		// An indexer outage is the one fault a title is not backed off for: it affects
		// every due title, so backing them all off idles the library on one hiccup.
		if errors.Is(err, ErrIndexerSearch) {
			return err
		}
		return errors.Join(err, s.backOffAfterFailure(ctx, title, now))
	}

	grabbed, held, err := s.grabPass(ctx, title, m, sweep, now, sourceSweep)
	// A pass that grabbed something is progress even if it ended badly, and its
	// successful grabs settle those items, so it records the ordinary cadence.
	if err != nil && grabbed == 0 {
		return errors.Join(err, s.backOffAfterFailure(ctx, title, now))
	}
	return errors.Join(err, s.writeSearchState(ctx, title, sweep, now, grabbed, held, hasFeed))
}

// backOffAfterFailure moves a failed pass to the back of the search queue. The
// due query is a small LIMIT ordered by next_search_at, so a title that keeps
// failing without this stays at the head of the queue and blocks every title
// behind it. last_searched_at does not move: nothing was searched,
// and moving it would put an already-aired episode outside airedSince's window.
func (s *Service) backOffAfterFailure(ctx context.Context, title db.Series, now time.Time) error {
	if ctx.Err() != nil {
		return nil
	}
	backoff := title.SearchBackoff + 1
	return s.setSearchState(ctx, db.SetTitleSearchStateParams{
		ID:             title.ID,
		LastSearchedAt: title.LastSearchedAt,
		SearchBackoff:  backoff,
		NextSearchAt:   nullTimestamp(now.Add(backoffDelay(int(backoff)))),
		SearchEpoch:    title.SearchEpoch,
	})
}

// passItems is the item list the matcher receives. In-flight and unaired items
// are passed as non-candidates while InLibrary keeps reporting the library
// alone: decide excludes non-candidates from the wanted set while maxItem spans
// them, so in-flight suppression comes out of the existing matcher and a batch
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

// grabPass walks the ranked candidates once, grabbing every eligible release
// whose items no earlier release covered. It returns how many releases were
// grabbed and the earliest moment a pin-held release becomes grabbable (zero when
// none is held). source names the entry point that drove it — the sweep or the
// feed poll — so a log line names which of the two acted.
//
// In notify-only (#116) the walk is the same walk — the one decision layer both
// entry points share — but every take dispatches a rehearsal event instead of
// grabbing, and the count returned is 0. So the search cadence is rehearsed and
// the grab-driven reset is not: a real grab makes a title due next tick, while
// a would-grab backs it off, because nothing settled and counting it would
// re-decide the same items every tick. Switching to on clears that (see
// settings.UpdateAutomation).
func (s *Service) grabPass(ctx context.Context, title db.Series, m Match, sweep []sweepItem, now time.Time, source passSource) (int, time.Time, error) {
	res, err := s.walkCandidates(ctx, title, m, sweep, now, source)
	idx := indexCandidates(m.Candidates)
	// A pass that stopped partway still decided something real, so what it did
	// decide is flushed on the error path too (#181).
	finalizeOutcomes(&res, idx, sweep, source)
	s.persistOutcomes(ctx, sweep, res.outcomes, source, now)
	if err != nil {
		return res.grabbed, time.Time{}, err
	}
	if res.rehearsed {
		// "Would have done nothing, and here's why" is the useful half of a
		// rehearsal — but only the sweep's, which searched for this title;
		// per-title silence is the feed page's normal state.
		if source == sourceSweep {
			s.rehearseNoAction(ctx, title, idx, sweep, res.covered)
		}
		return 0, res.held, nil
	}
	return res.grabbed, res.held, nil
}

// walkResult is what one walk of the ranked candidates decided. covered and
// outcomes are kept side by side rather than merged: covered runs per candidate
// on the feed's hot path. They match by invariant — an item is covered exactly
// when a settling outcome closed it (see walk_invariant_test.go).
type walkResult struct {
	grabbed   int
	held      time.Time
	covered   map[int]bool
	outcomes  outcomeSet
	rehearsed bool // notify-only was on, so nothing was sent to the download client
	complete  bool // false when the walk returned early and never examined the rest
}

// walkCandidates grabs every eligible release whose items no earlier release
// covered, in rank order. It is split from grabPass so there is one exit that
// acts on what the walk decided.
func (s *Service) walkCandidates(ctx context.Context, title db.Series, m Match, sweep []sweepItem, now time.Time, source passSource) (walkResult, error) {
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
		// set is Items minus the held items the upgrade policy excluded; a blocked
		// item is left uncovered, so a lower-ranked release that does
		// qualify -- its own release group's v2 -- is still tried this pass.
		take := c.TakeItems()
		if !c.Matched || !c.Eligible || len(take) == 0 {
			continue
		}
		// Records nothing: anyCovered is true on a single overlapping item, so the
		// rest of take was not contended, and recording it here would replace their
		// own refusal with a tentative that outranks it. finalizeOutcomes fills it in.
		if anyCovered(covered, take) {
			continue
		}
		if until, ok := s.pinHold(title, c, take, airs, now); ok {
			if res.held.IsZero() || until.Before(res.held) {
				res.held = until
			}
			// Marking the whole release covered delays items whose own window has
			// long closed, when a batch's anchor is its newest episode. Deliberate:
			// taking an old episode separately and the batch later is worse.
			markCovered(covered, take)
			res.outcomes.settle(take, outcome{
				kind:      OutcomePinHeld,
				release:   c.Release.Title,
				detail:    fmt.Sprintf("waiting for the pinned group %q", title.PinnedGroup.String),
				heldUntil: until,
			})
			if notifyOnly {
				s.dispatchRehearsal(ctx, title, take, c.Release.Title,
					fmt.Sprintf("would have waited: held %s for the pinned group %q",
						until.Sub(now).Round(time.Minute), title.PinnedGroup.String))
			}
			continue
		}
		if notifyOnly {
			s.log.Info("rehearsal: would have grabbed a release",
				"source", string(source), "title", title.ID, "release", c.Release.Title, "items", take)
			s.dispatchRehearsal(ctx, title, take, c.Release.Title, "would have grabbed")
			markCovered(covered, take)
			res.outcomes.settle(take, outcome{kind: OutcomeWouldGrab, release: c.Release.Title})
			res.grabbed++
			continue
		}
		if _, err := s.AutoGrab(ctx, title.ID, c, m.Items); err != nil {
			// Another grab already covers these items — in flight, or settled since this
			// pass read them. Leave them uncovered either way: an in-flight grab may
			// still fail, and a later pass must be free to retry them.
			if errors.Is(err, errItemsTaken) {
				res.outcomes.tentative(take, outcome{kind: OutcomeContended, release: c.Release.Title})
				continue
			}
			if !errors.Is(err, ErrDownloadAdd) {
				return res, err
			}
			// A dead download URL is this release's problem, not the title's: the
			// items stay unclaimed so the next-ranked release is still tried, and
			// only a client that keeps failing ends the pass. AutoGrab has already
			// recorded it if the release itself was at fault (#120).
			failed++
			res.outcomes.tentative(take, outcome{
				kind: OutcomeAddFailed, release: c.Release.Title, detail: err.Error(),
			})
			s.log.Warn("could not add a release; trying the next candidate",
				"source", string(source), "title", title.ID, "release", c.Release.Title, "err", err)
			if failed >= maxAddFailures {
				return res, fmt.Errorf("%d refused adds: %w", failed, err)
			}
			continue
		}
		s.log.Info("grabbed a release",
			"source", string(source), "title", title.ID, "release", c.Release.Title, "items", take)
		if d := s.clients.Notify(); d != nil {
			item := 0
			if len(take) == 1 {
				item = take[0]
			}
			d.Dispatch(ctx, notify.Event{
				Kind:         notify.KindGrabbed,
				Title:        title.Title,
				ItemNumber:   item,
				ItemKind:     domain.KindFor(domain.Format(title.Format)),
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

// finalizeOutcomes fills in what the candidate walk did not record: an uncovered item
// records the refused candidate that came closest to covering it, and failing
// that the pass records no_match — but only a sweep that ran to the end may. A
// hard return never examined the remaining candidates, and a feed poll examined
// one page covering the whole library rather than a search for this title, so
// either one recording no_match would overwrite a real refusal with a guess.
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
			// An eligible release covers it and the pass grabbed an overlapping one
			// first, so a later pass covers it and there is nothing to report.
			res.outcomes.tentative([]int{it.number}, outcome{kind: OutcomeDeferred})
		case res.complete && source == sourceSweep:
			res.outcomes.tentative([]int{it.number}, outcome{kind: OutcomeNoMatch})
		}
	}
}

// persistOutcomes writes one row per decided item. A failure only logs:
// sweepTitle backs a title off on a returned error with nothing grabbed, so
// surfacing a failed display-column write would move it down the search queue.
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
		// onto one row, as they already do in covered.
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
// set: an adapter renders this field as the event's detail, so a correct
// "would have grabbed" must not render as a blank where the reason should be.
func (s *Service) dispatchRehearsal(ctx context.Context, title db.Series, items []int, release, outcome string) {
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
		Title:        title.Title,
		ItemNumber:   item,
		ItemKind:     domain.KindFor(domain.Format(title.Format)),
		ReleaseTitle: release,
		Error:        outcome,
	})
}

// rehearseNoAction reports the wanted items a searched pass would not have
// grabbed, naming the best matched-but-refused candidate when there is one. It
// reports on what the candidate walk did not cover rather than on "nothing happened", so a
// title whose episode 1 was pin-held still reports that 2 and 3 went unmatched —
// the mismatch a rehearsal exists to surface.
func (s *Service) rehearseNoAction(ctx context.Context, title db.Series, idx passIndex, sweep []sweepItem, covered map[int]bool) {
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
	// cannot differ about which release came closest (#181).
	release, reason := idx.bestRefusal(wanted)
	if reason == "" {
		reason = "no matching release found"
	}
	s.dispatchRehearsal(ctx, title, wanted, release, "would have grabbed nothing: "+reason)
}

// writeSearchState records what the pass found. The write is guarded on the
// cadence read at selection, so a reset that landed mid-sweep is kept.
// The airing-aimed parts of the cadence exist only for the feedless world; with
// a feed, a missed broadcast is the feed poll's gap reset to recover (#100, #140).
func (s *Service) writeSearchState(ctx context.Context, title db.Series, sweep []sweepItem, now time.Time, grabbed int, held time.Time, hasFeed bool) error {
	upcoming := nextAiring(sweep, now)
	if hasFeed {
		upcoming = time.Time{}
	}

	backoff := title.SearchBackoff
	var next time.Time
	switch {
	case grabbed > 0:
		// Something landed, so more may be available: due again next tick.
		backoff = 0
	case !held.IsZero():
		// A pin-held item is never backed off past its own window. The pin delay stays
		// with the sweep either way: the release already exists, so no feed
		// poll will produce it sooner.
		backoff = 0
		next = earliest(held, upcoming)
	default:
		if !hasFeed && airedSince(sweep, title.LastSearchedAt, now) {
			backoff = 0
		}
		backoff++
		next = earliest(now.Add(backoffDelay(int(backoff))), upcoming)
	}

	return s.setSearchState(ctx, db.SetTitleSearchStateParams{
		ID:             title.ID,
		LastSearchedAt: sql.NullString{String: store.FormatTimestamp(now), Valid: true},
		SearchBackoff:  backoff,
		NextSearchAt:   nullTimestamp(next),
		SearchEpoch:    title.SearchEpoch,
	})
}

// setSearchState applies a cadence write and reports a lost epoch guard rather
// than discarding it: zero rows means a reset landed mid-sweep and was
// deliberately kept, or the title is gone. Neither is an error, but both explain
// a backoff that silently did not stick.
func (s *Service) setSearchState(ctx context.Context, p db.SetTitleSearchStateParams) error {
	rows, err := s.store.Q.SetTitleSearchState(ctx, p)
	if err != nil {
		return fmt.Errorf("write search cadence for title %d: %w", p.ID, err)
	}
	if rows == 0 {
		s.log.Debug("search cadence write skipped; the title was reset or removed mid-sweep",
			"title", p.ID, "epoch", p.SearchEpoch)
	}
	return nil
}

// loadSweepItems reads every wanted item with the grab state that decides
// whether it is worth searching for right now.
func (s *Service) loadSweepItems(ctx context.Context, titleID int64, now time.Time) ([]sweepItem, error) {
	rows, err := s.store.Q.ListWantedItemsWithGrabState(ctx, titleID)
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
		// re-open. An unextracted deferral and an in-flight grab are excluded.
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

// nextAiring is the earliest broadcast still in the future among monitored items
// not in the library, or the zero time when nothing is scheduled.
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

// earliest returns the sooner of two instants, ignoring zero values.
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

// pinHold reports when a candidate becomes grabbable, and whether it must be
// delayed (#62). Only another release group's release is ever delayed, and only
// while the window since the latest covered broadcast is still open — a covered
// item with no air date makes that window unmeasurable, so the delay does not
// apply rather than measuring from now, which would restart it on every restart.
func (s *Service) pinHold(title db.Series, c decide.Candidate, items []int, airs map[int]time.Time, now time.Time) (time.Time, bool) {
	if !title.PinnedGroup.Valid || title.PinnedGroup.String == "" || c.Pinned {
		return time.Time{}, false
	}
	delay := s.cfg.PinDelayDefault()
	if title.PinDelayHours.Valid {
		delay = domain.PinDelay(title.PinDelayHours.Int64)
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
