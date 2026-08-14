package acquire_test

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/acquire"
	"github.com/matthewdias/transpondarr/internal/core/domain"
	"github.com/matthewdias/transpondarr/internal/core/indexer"
	"github.com/matthewdias/transpondarr/internal/store"
	"github.com/matthewdias/transpondarr/internal/store/db"
)

// All fixtures use invented film, title and group names; only the naming
// structure under test is real.

// seedMovie inserts a monitored movie with its single wanted item. year 0 is
// "no year on record", the state automation must never grab in (#208).
func seedMovie(t *testing.T, st *store.Store, title string, year int64) int64 {
	t.Helper()
	ctx := context.Background()
	s, err := st.Q.CreateTitle(ctx, db.CreateTitleParams{
		Title: title, Format: "MOVIE", Monitored: 1, Year: year,
	})
	if err != nil {
		t.Fatalf("create movie series: %v", err)
	}
	if _, err := st.Q.CreateWantedItem(ctx, db.CreateWantedItemParams{
		SeriesID: s.ID, Kind: "movie",
		Number:    sql.NullInt64{Int64: 1, Valid: true},
		Monitored: 1,
	}); err != nil {
		t.Fatalf("create wanted item: %v", err)
	}
	return s.ID
}

// movieRelease builds a synthetic film release in the bracketed anime form.
func movieRelease(group, title string, year int) indexer.Release {
	return indexer.Release{
		Title:       fmt.Sprintf("[%s] %s (%d) [1080p]", group, title, year),
		DownloadURL: fmt.Sprintf("magnet:?xt=urn:btih:%s%d", group, year),
		Seeders:     100,
	}
}

// movieFeedEntry is movieRelease as a feed page entry.
func movieFeedEntry(group, title string, year int, published time.Time) indexer.FeedEntry {
	rel := movieRelease(group, title, year)
	return indexer.FeedEntry{
		Release:   rel,
		GUID:      fmt.Sprintf("guid-%s-%s-%d", group, title, year),
		Published: published,
	}
}

// The headline of #211: a monitored film is acquired unattended by the sweep,
// for one search, and its cadence resets exactly as an episode's does.
func TestSweepGrabsAWantedMovie(t *testing.T) {
	h := newSweep(t, []indexer.Release{movieRelease("ExampleSubs", "Sample Film", 2019)}, fakeConfig{})
	id := seedMovie(t, h.st, "Sample Film", 2019)

	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}

	if got := grabbedItemNumbers(t, h.st, id); len(got) != 1 || got[0] != 1 {
		t.Fatalf("grabbed items = %v, want the film's single item [1]", got)
	}
	if len(h.idx.Queries) != 1 {
		t.Errorf("indexer queried %d times for one film, want 1", len(h.idx.Queries))
	}
	if term := h.idx.Queries[0].Term; term != "Sample Film" {
		t.Errorf("search term = %q, want the film's title", term)
	}
	state := readSearchState(t, h.st, id)
	if state.backoff != 0 || state.nextSearchAt.Valid {
		t.Errorf("state = %+v, want backoff 0 and next_search_at NULL after a grab", state)
	}
	if !state.lastSearched.Valid {
		t.Error("last_searched_at not stamped")
	}
}

// A film nobody is seeding for climbs the same ladder as a title, rather than
// holding a slot at the head of the due queue and burning a search every tick.
func TestSweepMovieClimbsTheBackoffLadder(t *testing.T) {
	h := newSweep(t, nil, fakeConfig{})
	id := seedMovie(t, h.st, "Sample Film", 2019)

	before := time.Now()
	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	state := readSearchState(t, h.st, id)
	if state.backoff != 1 {
		t.Fatalf("backoff = %d, want 1 after the first empty pass", state.backoff)
	}
	wantNextSearchNear(t, state.nextSearchAt, before.Add(time.Hour))

	if _, err := h.st.DB.ExecContext(context.Background(),
		`UPDATE series SET search_backoff = 10, next_search_at = NULL WHERE id = ?`, id); err != nil {
		t.Fatalf("advance the backoff: %v", err)
	}
	before = time.Now()
	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	state = readSearchState(t, h.st, id)
	if state.backoff != 11 {
		t.Errorf("backoff = %d, want 11", state.backoff)
	}
	wantNextSearchNear(t, state.nextSearchAt, before.Add(24*time.Hour))
}

// A film makes a null air date the common case rather than the degraded one.
// Neither cadence helper may read that absence as a broadcast: airedSince must
// not reset the ladder and nextAiring must not clamp the next search.
func TestSweepMovieWithNoAirDateLeavesTheCadenceHelpersInert(t *testing.T) {
	now := time.Now()
	h := newSweep(t, nil, fakeConfig{})
	id := seedMovie(t, h.st, "Sample Film", 2019)
	if _, err := h.st.DB.ExecContext(context.Background(),
		`UPDATE series SET search_backoff = 6, last_searched_at = ?, next_search_at = NULL WHERE id = ?`,
		store.FormatTimestamp(now.Add(-3*time.Hour)), id); err != nil {
		t.Fatalf("seed a long backoff: %v", err)
	}

	before := time.Now()
	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	state := readSearchState(t, h.st, id)
	if state.backoff != 7 {
		t.Errorf("backoff = %d, want 7 — a null air date is not a broadcast to reset on", state.backoff)
	}
	wantNextSearchNear(t, state.nextSearchAt, before.Add(24*time.Hour))
}

// #224 gave films a date, which makes an announced one unaired and so not
// grabbable. That is right -- AniList's start date is the theatrical premiere,
// and nothing exists to find before it -- so the sweep must not spend a search
// on it. The Wanted page keeps showing it regardless; the two are separate
// questions, which is why this asserts only the acquisition half.
func TestSweepSkipsAnAnnouncedFilmUntilItsPremiere(t *testing.T) {
	h := newSweep(t, []indexer.Release{movieRelease("ExampleSubs", "Sample Film", 2027)}, fakeConfig{})
	id := seedMovie(t, h.st, "Sample Film", 2027)
	if _, err := h.st.DB.ExecContext(context.Background(),
		`UPDATE wanted_items SET airs_at = ? WHERE series_id = ?`,
		store.FormatTimestamp(time.Now().Add(90*24*time.Hour)), id); err != nil {
		t.Fatalf("date the film ahead: %v", err)
	}

	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}

	if len(h.idx.Queries) != 0 {
		t.Errorf("indexer queried %d times for an unreleased film, want 0", len(h.idx.Queries))
	}
	if got := grabbedItemNumbers(t, h.st, id); len(got) != 0 {
		t.Errorf("grabbed items = %v, want none before the premiere", got)
	}
}

// #208's amended rule, proved through automation: a film whose year is not yet
// on record still matches, so a manual grab stays free (PR #57), but the sweep
// never takes it -- and the pass stores the refusal so the Wanted page says why.
func TestSweepNeverGrabsANullYearMovieAndRecordsWhy(t *testing.T) {
	h := newSweep(t, []indexer.Release{movieRelease("ExampleSubs", "Sample Film", 2019)}, fakeConfig{})
	id := seedMovie(t, h.st, "Sample Film", 0)

	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}

	if got := grabbedItemNumbers(t, h.st, id); len(got) != 0 {
		t.Fatalf("grabbed %v for a film with no year on record, want nothing", got)
	}
	row := wantOutcome(t, h.st, id, 1, acquire.OutcomeDeclined)
	if row.Detail != "the movie has no year on record" {
		t.Errorf("detail = %q, want the null-year reason", row.Detail)
	}
	if row.ReleaseTitle != "[ExampleSubs] Sample Film (2019) [1080p]" {
		t.Errorf("release = %q, want the refused release named", row.ReleaseTitle)
	}
}

// A film has no measurable broadcast window, so the pin delay does not apply --
// rather than anchoring to now, which would restart the wait on every pass and
// never let another group's release through.
func TestSweepMovieIgnoresThePinDelayWithNoAirDate(t *testing.T) {
	h := newSweep(t, []indexer.Release{movieRelease("ExampleSubs", "Sample Film", 2019)},
		fakeConfig{pinDelay: 6 * time.Hour})
	id := seedMovie(t, h.st, "Sample Film", 2019)
	pinTitle(t, h.st, id, "OtherSubs", -1)

	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}

	if got := grabbedItemNumbers(t, h.st, id); len(got) != 1 || got[0] != 1 {
		t.Fatalf("grabbed items = %v, want [1] — an unmeasurable window is not a hold", got)
	}
	if row, ok := passOutcome(t, h.st, id, 1); ok && row.Outcome == acquire.OutcomePinHeld {
		t.Errorf("outcome = %s, want the film taken rather than held", row.Outcome)
	}
}

// The wrong grab both of movie mode's numeric gates are blind to: a numberless
// season pack of the film's parent title names no episode and carries no year,
// so nothing refuses it and the importer then hardlinks the title's episode 1
// into the Movies root under the film's name. Automation must decline it; the
// reason is stored, so the Wanted page says which release and why.
func TestSweepNeverGrabsAParentSeriesSeasonPackForAFilm(t *testing.T) {
	rel := indexer.Release{
		Title:       "[ExampleSubs] Placeholder Saga (Complete Series) [1080p]",
		DownloadURL: "magnet:?xt=urn:btih:completesaga",
		Seeders:     900,
	}
	h := newSweep(t, []indexer.Release{rel}, fakeConfig{})
	id := seedMovie(t, h.st, "Placeholder Saga: The Final", 2019)

	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if got := grabbedItemNumbers(t, h.st, id); len(got) != 0 {
		t.Fatalf("grabbed %v for the film, want nothing — that is the parent series' pack", got)
	}
	row := wantOutcome(t, h.st, id, 1, acquire.OutcomeDeclined)
	if row.ReleaseTitle != rel.Title {
		t.Errorf("release = %q, want the pack named", row.ReleaseTitle)
	}
	if row.Detail != "the release is a batch or season pack, which may be the series rather than the film" {
		t.Errorf("detail = %q, want the pack reason", row.Detail)
	}
}

// The deliberate cost of the rule above, stated as its own test: a genuine
// multi-part film release is withheld from automation too, because nothing can
// tell it from its parent title's pack. A human still takes it (PR #57).
func TestSweepWithholdsAFilmsOwnBatchTokenedRelease(t *testing.T) {
	rel := indexer.Release{
		Title:       "[ExampleSubs] Sample Film (2019) (Complete) [1080p][Dual Audio]",
		DownloadURL: "magnet:?xt=urn:btih:complete2019",
		Seeders:     80,
	}
	h := newSweep(t, []indexer.Release{rel}, fakeConfig{})
	id := seedMovie(t, h.st, "Sample Film", 2019)

	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if got := grabbedItemNumbers(t, h.st, id); len(got) != 0 {
		t.Errorf("grabbed items = %v, want nothing taken unattended", got)
	}
}

// The blocklist is format-neutral: a remembered film release degrades the sweep
// to the next-best one, exactly as it does for an episode (#118).
func TestSweepMovieSkipsABlocklistedRelease(t *testing.T) {
	top := movieRelease("TopSubs", "Sample Film", 2019)
	top.Seeders = 500
	next := movieRelease("NextSubs", "Sample Film", 2019)
	next.Seeders = 10

	h := newSweep(t, []indexer.Release{top, next}, fakeConfig{})
	id := seedMovie(t, h.st, "Sample Film", 2019)
	blockRelease(t, h.st, id, "tophash", top.Title, time.Now().Add(24*time.Hour))

	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	got := grabbedReleaseTitles(t, h.st, id)
	if len(got) != 1 || got[0] != next.Title {
		t.Fatalf("grabbed %v, want only the next-best film release %q", got, next.Title)
	}
}

// The feed is the other entry point onto the one decision layer: a film on the
// page is grabbed with no search spent at all.
func TestFeedPollGrabsAWantedMovie(t *testing.T) {
	h := newFeedPoll(t, []indexer.FeedEntry{
		movieFeedEntry("ExampleSubs", "Sample Film", 2019, time.Now().Add(-5*time.Minute)),
	}, fakeConfig{})
	id := seedMovie(t, h.st, "Sample Film", 2019)

	if err := h.svc.PollFeedOnce(context.Background()); err != nil {
		t.Fatalf("PollFeedOnce: %v", err)
	}
	if got := grabbedItemNumbers(t, h.st, id); len(got) != 1 || got[0] != 1 {
		t.Fatalf("grabbed items = %v, want the film's single item [1]", got)
	}
	if n := h.dl.AddCount(); n != 1 {
		t.Errorf("download Add called %d times, want 1", n)
	}
	// A poll writes no cadence: nothing was searched, and the grab settles the item.
	if state := readSearchState(t, h.st, id); state.lastSearched.Valid {
		t.Error("the feed poll wrote a search cadence for the film")
	}
}

// The null-year gate is a property of the decision layer, so it holds through
// the trigger that never spends a search either.
func TestFeedPollNeverGrabsANullYearMovie(t *testing.T) {
	h := newFeedPoll(t, []indexer.FeedEntry{
		movieFeedEntry("ExampleSubs", "Sample Film", 2019, time.Now().Add(-5*time.Minute)),
	}, fakeConfig{})
	id := seedMovie(t, h.st, "Sample Film", 0)

	if err := h.svc.PollFeedOnce(context.Background()); err != nil {
		t.Fatalf("PollFeedOnce: %v", err)
	}
	if got := grabbedItemNumbers(t, h.st, id); len(got) != 0 {
		t.Fatalf("grabbed %v for a film with no year on record, want nothing", got)
	}
	row := wantOutcome(t, h.st, id, 1, acquire.OutcomeDeclined)
	if row.Source != "feed" {
		t.Errorf("source = %q, want feed", row.Source)
	}
	if row.Detail != "the movie has no year on record" {
		t.Errorf("detail = %q, want the null-year reason", row.Detail)
	}
}

// #209's numeric identity guard, end to end through the trigger that would have
// acted on it unattended. titleBelongs is fuzzy containment, so a long-runner
// sharing a name prefix with a film reaches the movie path; unrefused, its
// episode 250 is grabbed into the film's single item with nobody watching.
func TestFeedPollDoesNotGrabALongRunnersEpisodeForAFilm(t *testing.T) {
	h := newFeedPoll(t, []indexer.FeedEntry{
		feedEntry("Placeholder Saga", 250, time.Now().Add(-5*time.Minute)),
	}, fakeConfig{})
	film := seedMovie(t, h.st, "Placeholder Saga: The Final", 2019)
	saga := seedSweep(t, h.st, "Placeholder Saga", true,
		sweepItem{number: 1}, sweepItem{number: 12})

	if err := h.svc.PollFeedOnce(context.Background()); err != nil {
		t.Fatalf("PollFeedOnce: %v", err)
	}
	if got := grabbedItemNumbers(t, h.st, film); len(got) != 0 {
		t.Fatalf("grabbed %v for the film, want nothing — that is a long-runner's episode", got)
	}
	// The long-runner refuses it too, on its own maxItem: the point is that no
	// entry in the library took it, so the whole page was declined.
	if got := grabbedItemNumbers(t, h.st, saga); len(got) != 0 {
		t.Errorf("grabbed %v for the long-runner, want nothing past its range", got)
	}
	if n := h.dl.AddCount(); n != 0 {
		t.Errorf("download Add called %d times, want 0", n)
	}
}

// The claim registry is format-neutral, and a film is the case where both
// triggers see exactly one item: the two phase-locked jobs must still produce
// one add.
func TestConcurrentSweepAndFeedPollGrabAMovieOnce(t *testing.T) {
	h := newFeedPoll(t, []indexer.FeedEntry{
		movieFeedEntry("ExampleSubs", "Sample Film", 2019, time.Now().Add(-5*time.Minute)),
	}, fakeConfig{})
	id := seedMovie(t, h.st, "Sample Film", 2019)

	entered, release := blockFirstAdd(h.dl)
	ctx := context.Background()

	var wg sync.WaitGroup
	var pollErr error
	wg.Go(func() { pollErr = h.svc.PollFeedOnce(ctx) })

	<-entered // the feed poll now holds the claim, inside the client
	sweepErr := h.svc.SweepOnce(ctx)
	release()
	wg.Wait()

	if pollErr != nil || sweepErr != nil {
		t.Fatalf("poll err = %v, sweep err = %v", pollErr, sweepErr)
	}
	if n := h.dl.AddCount(); n != 1 {
		t.Errorf("download Add called %d times, want exactly 1", n)
	}
	if got := grabbedItemNumbers(t, h.st, id); len(got) != 1 || got[0] != 1 {
		t.Errorf("grabbed items = %v, want [1]", got)
	}
}

// Notify-only rehearses a film like any other take: the release is named and
// nothing reaches the download client.
func TestNotifyOnlySweepReportsAWouldGrabMovie(t *testing.T) {
	h := newRehearsal(t, []indexer.Release{movieRelease("ExampleSubs", "Sample Film", 2019)},
		fakeConfig{notifyOnly: true})
	id := seedMovie(t, h.st, "Sample Film", 2019)

	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}

	ev := wantRehearsalEventOfKind(t, h.fn, domain.KindMovie)
	if ev.Title != "Sample Film" {
		t.Errorf("title = %q", ev.Title)
	}
	if ev.ReleaseTitle != "[ExampleSubs] Sample Film (2019) [1080p]" {
		t.Errorf("release = %q, want the film it would have grabbed", ev.ReleaseTitle)
	}
	if ev.ItemNumber != 1 {
		t.Errorf("item = %d, want the film's single item", ev.ItemNumber)
	}
	if ev.Error != "would have grabbed" {
		t.Errorf("outcome = %q, want the would-grab spelled out", ev.Error)
	}
	if len(h.dl.Adds) != 0 {
		t.Errorf("a rehearsal reached the download client: %+v", h.dl.Adds)
	}
	if got := grabbedItemNumbers(t, h.st, id); len(got) != 0 {
		t.Errorf("a rehearsal wrote grabs %v", got)
	}
}

// A rehearsal reports a refusal as readily as a take, so the null-year gate is
// visible before automation is switched on rather than after it grabs nothing.
func TestNotifyOnlySweepReportsANullYearMovieRefusal(t *testing.T) {
	h := newRehearsal(t, []indexer.Release{movieRelease("ExampleSubs", "Sample Film", 2019)},
		fakeConfig{notifyOnly: true})
	seedMovie(t, h.st, "Sample Film", 0)

	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}

	ev := wantRehearsalEventOfKind(t, h.fn, domain.KindMovie)
	if ev.Error != "would have grabbed nothing: the movie has no year on record" {
		t.Errorf("outcome = %q, want the null-year refusal reported", ev.Error)
	}
	if ev.ItemNumber != 1 {
		t.Errorf("item = %d, want the film's single item", ev.ItemNumber)
	}
}
