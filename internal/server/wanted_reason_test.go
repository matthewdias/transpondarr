package server

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/acquire"
	"github.com/matthewdias/transpondarr/internal/core/settings"
)

// Each reason tier is a ranking, not a set: a slot states the one fact that
// most explains its scope, so the order between reasons is the whole contract.
// The tiers never compete with each other -- the page shows all three at once.

func TestGlobalReasonRanking(t *testing.T) {
	cases := []struct {
		name         string
		indexerReady bool
		mode         settings.AutomationMode
		want         string
	}{
		{"nothing wrong", true, settings.AutomationOn, ""},
		{"automation off", true, settings.AutomationOff, reasonAutomationOff},
		{"notify-only", true, settings.AutomationNotifyOnly, reasonNotifyOnly},
		{"no indexer outranks automation state", false, settings.AutomationOff, reasonNoIndexer},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := globalReason(tc.indexerReady, tc.mode); got != tc.want {
				t.Errorf("globalReason = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSeriesReasonRanking(t *testing.T) {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	// ready is a series with nothing wrong: monitored, searched, due now.
	ready := func() seriesFacts {
		return seriesFacts{Monitored: true, LastSearchedAt: now.Add(-time.Hour)}
	}

	cases := []struct {
		name string
		with func(*seriesFacts)
		want string
	}{
		{"due now", func(*seriesFacts) {}, reasonSearchDue},
		{"backing off", func(f *seriesFacts) { f.NextSearchAt = now.Add(2 * time.Hour) }, reasonSearchBackoff},
		{"never searched", func(f *seriesFacts) { f.LastSearchedAt = time.Time{} }, reasonNeverSearched},
		{"blocklisted outranks the queue", func(f *seriesFacts) {
			f.BlockedReleases = 3
			f.NextSearchAt = now.Add(2 * time.Hour)
		}, reasonBlocklisted},
		{"unmonitored outranks everything", func(f *seriesFacts) {
			f.Monitored = false
			f.BlockedReleases = 3
		}, reasonUnmonitored},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := ready()
			tc.with(&f)
			if got := seriesReason(f, now); got != tc.want {
				t.Errorf("seriesReason = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestItemReasonRanking(t *testing.T) {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	aired := now.Add(-24 * time.Hour)
	pass := func(outcome string) passFacts {
		return passFacts{Outcome: outcome, RecordedAt: now.Add(-2 * time.Hour)}
	}
	cases := []struct {
		name     string
		f        itemFacts
		want     string
		fromPass bool
	}{
		{"nothing item-level", itemFacts{AirsAt: aired}, "", false},
		{"failed grab", itemFacts{AirsAt: aired, GrabFailed: true}, reasonGrabFailed, false},
		{"unaired outranks a failed grab",
			itemFacts{AirsAt: now.Add(time.Hour), GrabFailed: true}, reasonUnaired, false},
		{"declined", itemFacts{AirsAt: aired, Pass: pass(acquire.OutcomeDeclined)}, reasonDeclined, true},
		{"nothing matched", itemFacts{AirsAt: aired, Pass: pass(acquire.OutcomeNoMatch)}, reasonNoMatch, true},
		{"pin held", itemFacts{AirsAt: aired, Pass: pass(acquire.OutcomePinHeld)}, reasonPinHeld, true},
		{"would grab", itemFacts{AirsAt: aired, Pass: pass(acquire.OutcomeWouldGrab)}, reasonWouldGrab, true},
		{"add failed", itemFacts{AirsAt: aired, Pass: pass(acquire.OutcomeAddFailed)}, reasonAddFailed, true},
		// A decline is standing and user-actionable; a failure has already been
		// handled -- the item reverted to wanted and the group says blocklisted.
		{"a pass answer outranks a failed grab", itemFacts{
			AirsAt: aired, GrabFailed: true, GrabbedAt: now.Add(-6 * time.Hour),
			Pass: pass(acquire.OutcomeDeclined),
		}, reasonDeclined, true},
		{"unaired outranks a pass answer",
			itemFacts{AirsAt: now.Add(time.Hour), Pass: pass(acquire.OutcomeDeclined)}, reasonUnaired, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, fromPass := itemReason(tc.f, now)
			if got != tc.want || fromPass != tc.fromPass {
				t.Errorf("itemReason = %q/%t, want %q/%t", got, fromPass, tc.want, tc.fromPass)
			}
		})
	}
}

// The stored set and the surfaced set differ on purpose. grabbed is only the
// tombstone that invalidates an older refusal -- a listed item's grab plainly
// did not hold, and grab_failed owns that row -- and contention's honest
// message is "the queue is working", which the group tier already says.
func TestGrabbedAndContendedSurfaceNothing(t *testing.T) {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	for _, outcome := range []string{acquire.OutcomeGrabbed, acquire.OutcomeContended} {
		f := itemFacts{
			AirsAt: now.Add(-24 * time.Hour),
			Pass:   passFacts{Outcome: outcome, RecordedAt: now.Add(-time.Hour)},
		}
		if got, fromPass := itemReason(f, now); got != "" || fromPass {
			t.Errorf("%s surfaced as %q/%t, want nothing", outcome, got, fromPass)
		}
	}
}

// The suppression guard, which is exactly equivalent to ranking on recency: a
// pass only writes for a grabbable item and an item is not grabbable while its
// grab is live, so an outcome older than the grab beside it can only be a
// refusal the grab has already answered.
func TestAPassAnswerOlderThanItsGrabIsSuppressed(t *testing.T) {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	stale := itemFacts{
		AirsAt: now.Add(-48 * time.Hour), GrabFailed: true, GrabbedAt: now.Add(-2 * time.Hour),
		Pass: passFacts{Outcome: acquire.OutcomeDeclined, RecordedAt: now.Add(-6 * time.Hour)},
	}
	if got, fromPass := itemReason(stale, now); got != reasonGrabFailed || fromPass {
		t.Errorf("itemReason = %q/%t, want the failure: the refusal predates the grab", got, fromPass)
	}

	fresh := stale
	fresh.Pass.RecordedAt = now.Add(-time.Hour)
	if got, fromPass := itemReason(fresh, now); got != reasonDeclined || !fromPass {
		t.Errorf("itemReason = %q/%t, want the refusal recorded after the grab failed", got, fromPass)
	}

	// The tie is the pass that made the grab: it stamps both in the same second.
	tied := stale
	tied.Pass.RecordedAt = stale.GrabbedAt
	if got, _ := itemReason(tied, now); got != reasonGrabFailed {
		t.Errorf("itemReason = %q, want the failure on a same-instant answer", got)
	}
}

// A null air date is not a future one. AniList's coverage thins out badly before
// ~2015 and skips episodes even for modern titles, so an item with no schedule
// is searchable -- the sweep's own reading -- and must never read as unaired.
func TestItemReasonTreatsNoAirDateAsSearchable(t *testing.T) {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	if got, _ := itemReason(itemFacts{}, now); got != "" {
		t.Errorf("itemReason = %q, want none: an unscheduled item is searchable", got)
	}
}

// The DTO's enum is the contract the generated frontend types are built from,
// so a new outcome that surfaces must reach it or the UI gets a reason it has
// no label for.
func TestEveryPassReasonIsInTheDTOEnum(t *testing.T) {
	field, ok := reflect.TypeOf(missingItemDTO{}).FieldByName("Reason")
	if !ok {
		t.Fatal("missingItemDTO has no Reason field")
	}
	listed := map[string]bool{}
	for _, v := range strings.Split(field.Tag.Get("enum"), ",") {
		listed[v] = true
	}
	for _, outcome := range acquire.AllOutcomes {
		r := passReason(outcome)
		if r != "" && !listed[r] {
			t.Errorf("outcome %q surfaces as %q, which the reason enum does not list", outcome, r)
		}
	}
}
