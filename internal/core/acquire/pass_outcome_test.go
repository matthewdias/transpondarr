package acquire_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/acquire"
	"github.com/matthewdias/transpondarr/internal/core/indexer"
	"github.com/matthewdias/transpondarr/internal/store"
	"github.com/matthewdias/transpondarr/internal/store/db"
)

// passOutcome reads what the last pass recorded about one item, reporting
// whether anything was recorded at all -- an absent row is a real answer.
func passOutcome(t *testing.T, st *store.Store, titleID int64, number int) (db.PassOutcome, bool) {
	t.Helper()
	ctx := context.Background()
	items, err := st.Q.ListWantedItems(ctx, titleID)
	if err != nil {
		t.Fatalf("list items: %v", err)
	}
	for _, it := range items {
		if int(it.Number.Int64) != number {
			continue
		}
		row, err := st.Q.GetPassOutcome(ctx, it.ID)
		if errors.Is(err, sql.ErrNoRows) {
			return db.PassOutcome{}, false
		}
		if err != nil {
			t.Fatalf("read pass outcome for item %d: %v", number, err)
		}
		return row, true
	}
	t.Fatalf("no item %d on series %d", number, titleID)
	return db.PassOutcome{}, false
}

func wantOutcome(t *testing.T, st *store.Store, titleID, number int64, kind string) db.PassOutcome {
	t.Helper()
	row, ok := passOutcome(t, st, titleID, int(number))
	if !ok {
		t.Fatalf("episode %d recorded no outcome, want %s", number, kind)
	}
	if row.Outcome != kind {
		t.Fatalf("episode %d outcome = %q, want %s", number, row.Outcome, kind)
	}
	return row
}

// The headline of #181: a pass that found releases and turned them all down
// says so, naming the release and the reason -- the case that was previously
// indistinguishable from "nothing has looked at this yet".
func TestSweepRecordsADeclinedRelease(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	h := newSweep(t, []indexer.Release{episodeRelease("Placeholder Saga", 3)}, fakeConfig{})
	if _, err := h.st.DB.ExecContext(context.Background(),
		`UPDATE quality_profiles SET min_score = 9000 WHERE id = 1`); err != nil {
		t.Fatalf("raise the profile floor: %v", err)
	}
	id := seedSweep(t, h.st, "Placeholder Saga", true, sweepItem{number: 3, airsAt: &past})

	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}

	row := wantOutcome(t, h.st, id, 3, acquire.OutcomeDeclined)
	if row.ReleaseTitle != "[ExampleSubs] Placeholder Saga - 03 [1080p]" {
		t.Errorf("release = %q, want the refused release named", row.ReleaseTitle)
	}
	if row.Detail == "" {
		t.Error("detail is empty; a decline must carry why it was refused")
	}
	if row.Source != "sweep" {
		t.Errorf("source = %q, want sweep", row.Source)
	}
	if row.RecordedAt == "" {
		t.Error("recorded_at is empty; the badge is dated or it reads as now")
	}
	if row.HeldUntil.Valid {
		t.Errorf("held_until = %q on a decline, want none", row.HeldUntil.String)
	}
}

// Blame is per item, not per pass: a pack and a single covering different
// episodes are different answers, and the coverage tier that ranks the pack
// first for grab efficiency says nothing about either episode's near miss.
func TestSweepBlamesTheReleaseCoveringEachItem(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	// The pack ranks first on decide's coverage tier and the single beats it on
	// seeders, so a blame that inherited the ranking would name the pack twice.
	single := episodeRelease("Placeholder Saga", 3)
	single.Seeders = 999
	h := newSweep(t, []indexer.Release{packRelease("Placeholder Saga"), single}, fakeConfig{})
	if _, err := h.st.DB.ExecContext(context.Background(),
		`UPDATE quality_profiles SET min_score = 9000 WHERE id = 1`); err != nil {
		t.Fatalf("raise the profile floor: %v", err)
	}
	items := make([]sweepItem, 0, 6)
	for n := 1; n <= 6; n++ {
		items = append(items, sweepItem{number: n, airsAt: &past})
	}
	id := seedSweep(t, h.st, "Placeholder Saga", true, items...)

	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}

	if got := wantOutcome(t, h.st, id, 3, acquire.OutcomeDeclined).ReleaseTitle; got != single.Title {
		t.Errorf("episode 3 blamed %q, want the single covering it (%q)", got, single.Title)
	}
	if got := wantOutcome(t, h.st, id, 5, acquire.OutcomeDeclined).ReleaseTitle; !strings.Contains(got, "[Batchers]") {
		t.Errorf("episode 5 blamed %q, want the pack -- nothing else covers it", got)
	}
}

// A pin hold never reaches the refusal tail (it marks its items covered), so
// without its own outcome the best answer an item could carry would be lost.
func TestSweepRecordsAPinHoldWithItsWindow(t *testing.T) {
	aired := time.Now().Add(-time.Hour)
	h := newSweep(t, []indexer.Release{episodeRelease("Placeholder Saga", 3)},
		fakeConfig{pinDelay: 6 * time.Hour})
	id := seedSweep(t, h.st, "Placeholder Saga", true, sweepItem{number: 3, airsAt: &aired})
	pinTitle(t, h.st, id, "OtherSubs", -1)

	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}

	row := wantOutcome(t, h.st, id, 3, acquire.OutcomePinHeld)
	if row.ReleaseTitle == "" {
		t.Error("a hold must name the release it is waiting out")
	}
	if !strings.Contains(row.Detail, "OtherSubs") {
		t.Errorf("detail = %q, want the pinned group named", row.Detail)
	}
	if !row.HeldUntil.Valid {
		t.Fatal("held_until is NULL; the row has to say how long the wait runs")
	}
	until, err := store.ParseTimestamp(row.HeldUntil.String)
	if err != nil {
		t.Fatalf("parse held_until %q: %v", row.HeldUntil.String, err)
	}
	if d := until.Sub(aired.Add(6 * time.Hour).UTC()); d > time.Minute || d < -time.Minute {
		t.Errorf("held_until = %s, want ~%s", until, aired.Add(6*time.Hour).UTC())
	}
}

// A search that turned up nothing is a fact worth stating, and only a search
// may state it.
func TestSweepRecordsNoMatchWhenTheSearchCoveredNothing(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	h := newSweep(t, nil, fakeConfig{})
	id := seedSweep(t, h.st, "Placeholder Saga", true, sweepItem{number: 3, airsAt: &past})

	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	row := wantOutcome(t, h.st, id, 3, acquire.OutcomeNoMatch)
	if row.ReleaseTitle != "" {
		t.Errorf("release = %q, want none: nothing matched", row.ReleaseTitle)
	}
}

// A feed page is ~100 entries covering the whole library, not a search for this
// title, so it has no standing to say nothing matched -- that write would
// clobber a sweep's real refusal on every poll.
func TestFeedPollNeverRecordsNoMatch(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	h := newFeedPoll(t, []indexer.FeedEntry{
		feedEntry("Unrelated Show", 9, time.Now().Add(-10*time.Minute)),
	}, fakeConfig{})
	id := seedSweep(t, h.st, "Placeholder Saga", true, sweepItem{number: 3, airsAt: &past})

	if err := h.svc.PollFeedOnce(context.Background()); err != nil {
		t.Fatalf("PollFeedOnce: %v", err)
	}
	if row, ok := passOutcome(t, h.st, id, 3); ok {
		t.Errorf("the feed poll recorded %+v for a series its page said nothing about", row)
	}
}

// What the feed does decide is real, and is stored under its own source so the
// reader can tell the two apart.
func TestFeedPollRecordsItsOwnRefusalAsFeedSourced(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	h := newFeedPoll(t, []indexer.FeedEntry{
		feedEntry("Placeholder Saga", 3, time.Now().Add(-10*time.Minute)),
	}, fakeConfig{})
	if _, err := h.st.DB.ExecContext(context.Background(),
		`UPDATE quality_profiles SET min_score = 9000 WHERE id = 1`); err != nil {
		t.Fatalf("raise the profile floor: %v", err)
	}
	id := seedSweep(t, h.st, "Placeholder Saga", true, sweepItem{number: 3, airsAt: &past})

	if err := h.svc.PollFeedOnce(context.Background()); err != nil {
		t.Fatalf("PollFeedOnce: %v", err)
	}
	row := wantOutcome(t, h.st, id, 3, acquire.OutcomeDeclined)
	if row.Source != "feed" {
		t.Errorf("source = %q, want feed", row.Source)
	}
}

// The row is the last pass's answer, so a grab supersedes an earlier refusal
// rather than sitting beside it.
func TestAGrabOverwritesAnEarlierRefusal(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	h := newSweep(t, []indexer.Release{episodeRelease("Placeholder Saga", 3)}, fakeConfig{})
	ctx := context.Background()
	if _, err := h.st.DB.ExecContext(ctx,
		`UPDATE quality_profiles SET min_score = 9000 WHERE id = 1`); err != nil {
		t.Fatalf("raise the profile floor: %v", err)
	}
	id := seedSweep(t, h.st, "Placeholder Saga", true, sweepItem{number: 3, airsAt: &past})

	if err := h.svc.SweepOnce(ctx); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	wantOutcome(t, h.st, id, 3, acquire.OutcomeDeclined)

	if _, err := h.st.DB.ExecContext(ctx,
		`UPDATE quality_profiles SET min_score = 0 WHERE id = 1`); err != nil {
		t.Fatalf("lower the profile floor: %v", err)
	}
	makeTitleDue(t, h.st, id)
	if err := h.svc.SweepOnce(ctx); err != nil {
		t.Fatalf("second SweepOnce: %v", err)
	}
	wantOutcome(t, h.st, id, 3, acquire.OutcomeGrabbed)
}

// The bug the eligible-first ranking hides: rehearseNoAction requires
// !c.Eligible, so it walks past a candidate lost to claim contention and blames
// the profile for what was contention. Running the walk on every pass is what
// exposes it, so contention gets its own outcome.
func TestContentionIsRecordedAsContendedNotDeclined(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	fromFeed := episodeRelease("Placeholder Saga", 3)
	fromFeed.DownloadURL = "magnet:?xt=urn:btih:fromfeed"
	fromSearch := episodeRelease("Placeholder Saga", 3)
	fromSearch.DownloadURL = "magnet:?xt=urn:btih:fromsearch"

	h := newFeedPoll(t, []indexer.FeedEntry{
		{Release: fromFeed, GUID: "guid-feed", Published: time.Now()},
	}, fakeConfig{})
	h.feed.Releases = []indexer.Release{fromSearch}
	id := seedSweep(t, h.st, "Placeholder Saga", true, sweepItem{number: 3, airsAt: &past})

	ctx := context.Background()
	// The poll lands and takes item 3 while the sweep is out on its search,
	// after the sweep has already read the item as grabbable.
	var once sync.Once
	h.feed.SearchHook = func(indexer.Query) {
		once.Do(func() {
			if err := h.svc.PollFeedOnce(ctx); err != nil {
				t.Errorf("PollFeedOnce: %v", err)
			}
		})
	}
	if err := h.svc.SweepOnce(ctx); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}

	row := wantOutcome(t, h.st, id, 3, acquire.OutcomeContended)
	if row.Source != "sweep" {
		t.Errorf("source = %q, want the sweep that lost the race", row.Source)
	}
}

// A client that refused the add is not a release the profile turned down, and
// the difference is the whole point of the column.
func TestAnAddFailureIsRecordedAgainstItsRelease(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	dead := episodeRelease("Placeholder Saga", 3)
	h := newSweep(t, []indexer.Release{dead}, fakeConfig{})
	h.dl.FailURLs = map[string]error{dead.DownloadURL: errors.New("404 fetching .torrent")}
	id := seedSweep(t, h.st, "Placeholder Saga", true, sweepItem{number: 3, airsAt: &past})

	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	row := wantOutcome(t, h.st, id, 3, acquire.OutcomeAddFailed)
	if row.ReleaseTitle != dead.Title {
		t.Errorf("release = %q, want the release the client refused", row.ReleaseTitle)
	}
	if row.Detail == "" {
		t.Error("detail is empty; an add failure must carry what the client said")
	}
}

// A pass that gave up partway never examined the remaining candidates, so
// claiming nothing matched for an item it did not reach would be a lie. What it
// did decide is real and still lands.
func TestAPartialPassRecordsWhatItDecidedAndNoNoMatch(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	var releases []indexer.Release
	items := []sweepItem{}
	for n := 1; n <= 3; n++ {
		releases = append(releases, episodeRelease("Placeholder Saga", n))
		items = append(items, sweepItem{number: n, airsAt: &past})
	}
	// Episode 9 is wanted and nothing covers it; the pass ends before its turn.
	items = append(items, sweepItem{number: 9, airsAt: &past})

	h := newSweep(t, releases, fakeConfig{})
	h.dl.Err = errors.New("connection refused")
	id := seedSweep(t, h.st, "Placeholder Saga", true, items...)

	if err := h.svc.SweepOnce(context.Background()); err == nil {
		t.Fatal("SweepOnce returned nil, want the client failure surfaced")
	}
	for n := 1; n <= 3; n++ {
		wantOutcome(t, h.st, id, int64(n), acquire.OutcomeAddFailed)
	}
	if row, ok := passOutcome(t, h.st, id, 9); ok {
		t.Errorf("episode 9 recorded %+v; a pass that stopped early cannot say nothing matched", row)
	}
}

// #116's rehearsal gets the durable home it never had: the decisions land in the
// column instead of scrolling past in whatever notifier was configured.
func TestNotifyOnlyRecordsWouldGrab(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	h := newRehearsal(t, []indexer.Release{episodeRelease("Placeholder Saga", 5)},
		fakeConfig{notifyOnly: true})
	id := seedSweep(t, h.st, "Placeholder Saga", true, sweepItem{number: 5, airsAt: &past})

	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	row := wantOutcome(t, h.st, id, 5, acquire.OutcomeWouldGrab)
	if row.ReleaseTitle != "[ExampleSubs] Placeholder Saga - 05 [1080p]" {
		t.Errorf("release = %q, want the release it would have grabbed", row.ReleaseTitle)
	}
}

// The notification and the stored row are derived from one selection, so a user
// reading either gets the same answer.
func TestTheRehearsalBlamesTheReleaseTheRowStores(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	h := newRehearsal(t, []indexer.Release{episodeRelease("Placeholder Saga", 1)},
		fakeConfig{notifyOnly: true})
	if _, err := h.st.DB.ExecContext(context.Background(),
		`UPDATE quality_profiles SET min_score = 9000 WHERE id = 1`); err != nil {
		t.Fatalf("raise the profile floor: %v", err)
	}
	id := seedSweep(t, h.st, "Placeholder Saga", true,
		sweepItem{number: 1, airsAt: &past}, sweepItem{number: 2, airsAt: &past})

	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	ev := wantRehearsalEvent(t, h.fn)
	row := wantOutcome(t, h.st, id, 1, acquire.OutcomeDeclined)
	if ev.ReleaseTitle != row.ReleaseTitle {
		t.Errorf("the rehearsal blamed %q and the row stores %q; they must not disagree",
			ev.ReleaseTitle, row.ReleaseTitle)
	}
	// Episode 2 is covered by nothing at all, so it is the searched pass' own
	// "nothing matched" rather than an inherited blame.
	wantOutcome(t, h.st, id, 2, acquire.OutcomeNoMatch)
}

// The upgrade pool is grabbable and had at once (#97). Those rows can never be
// read back by the Missing listing, so writing them is dead weight on the exact
// hot path the one-row-per-item bound exists to protect.
func TestAnUpgradePoolItemRecordsNoOutcome(t *testing.T) {
	h := newFeedPoll(t, []indexer.FeedEntry{
		feedEntry("Placeholder Saga", 3, time.Now().Add(-10*time.Minute)),
	}, fakeConfig{})
	enableUpgrades(t, h.st, 9000)
	if _, err := h.st.DB.ExecContext(context.Background(),
		`UPDATE quality_profiles SET min_score = 9000 WHERE id = 1`); err != nil {
		t.Fatalf("raise the profile floor: %v", err)
	}
	id := seedSweep(t, h.st, "Placeholder Saga", true,
		sweepItem{number: 3, inLibrary: true, heldTitle: heldSD, grab: "imported"})

	if err := h.svc.PollFeedOnce(context.Background()); err != nil {
		t.Fatalf("PollFeedOnce: %v", err)
	}
	if row, ok := passOutcome(t, h.st, id, 3); ok {
		t.Errorf("a held item recorded %+v; the listing can never read it back", row)
	}
}

// One row per item, whatever the pass count: the table is bounded by
// wanted_items, which is what makes a write on every pass affordable.
func TestRepeatedPassesKeepOneRowPerItem(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	h := newSweep(t, nil, fakeConfig{})
	id := seedSweep(t, h.st, "Placeholder Saga", true,
		sweepItem{number: 1, airsAt: &past}, sweepItem{number: 2, airsAt: &past})

	for i := range 3 {
		makeTitleDue(t, h.st, id)
		if err := h.svc.SweepOnce(context.Background()); err != nil {
			t.Fatalf("SweepOnce %d: %v", i, err)
		}
	}
	var rows int
	if err := h.st.DB.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM pass_outcomes`).Scan(&rows); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if rows != 2 {
		t.Errorf("stored rows after three passes over two items = %d, want 2", rows)
	}
}

// An overlapping candidate is not contention. anyCovered trips on a single
// shared item, so the rest of the release was merely deferred to a later pass --
// and claiming it would bury each item's own refusal, which is the one thing
// this table exists to show. A batch beside weekly singles is a normal search
// result, not a corner.
func TestAnOverlappingBatchDoesNotSilenceItsItemsOwnRefusal(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	// 480p is hard-excluded below, so only episode 2's single is refused; the
	// pack stays eligible and overlaps episode 1, which the single takes first.
	refused := episodeRelease("Placeholder Saga", 2)
	refused.Title = "[ExampleSubs] Placeholder Saga - 02 [480p]"
	h := newSweep(t, []indexer.Release{
		episodeRelease("Placeholder Saga", 1), packRelease("Placeholder Saga"), refused,
	}, fakeConfig{})
	if _, err := h.st.DB.ExecContext(context.Background(),
		`UPDATE quality_profiles SET hard_excludes = '["480p"]' WHERE id = 1`); err != nil {
		t.Fatalf("exclude 480p: %v", err)
	}
	items := make([]sweepItem, 0, 6)
	for n := 1; n <= 6; n++ {
		items = append(items, sweepItem{number: n, airsAt: &past})
	}
	id := seedSweep(t, h.st, "Placeholder Saga", true, items...)
	// Pinning the singles' group ranks episode 1's single above the wider pack,
	// so the pack is the one that overlaps and is skipped.
	if _, err := h.st.DB.ExecContext(context.Background(),
		`UPDATE series SET pinned_group = 'ExampleSubs' WHERE id = ?`, id); err != nil {
		t.Fatalf("pin the group: %v", err)
	}

	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}

	// Episode 2's refusal is user-actionable and must survive the overlap.
	row := wantOutcome(t, h.st, id, 2, acquire.OutcomeDeclined)
	if !strings.Contains(row.Detail, "excluded by the profile") {
		t.Errorf("episode 2 detail = %q, want the exclusion that refused it", row.Detail)
	}
	// Episode 3 has no refusal of its own: an eligible pack covers it and this
	// pass took an overlapping release first, so it is next pass's, not a miss.
	if got := wantOutcome(t, h.st, id, 3, acquire.OutcomeDeferred); got.Outcome != acquire.OutcomeDeferred {
		t.Errorf("episode 3 outcome = %q", got.Outcome)
	}
}
