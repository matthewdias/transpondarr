package server

import (
	"testing"
	"time"

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
	cases := []struct {
		name string
		f    itemFacts
		want string
	}{
		{"nothing item-level", itemFacts{AirsAt: now.Add(-24 * time.Hour)}, ""},
		{"failed grab", itemFacts{AirsAt: now.Add(-24 * time.Hour), GrabFailed: true}, reasonGrabFailed},
		{"unaired outranks a failed grab", itemFacts{AirsAt: now.Add(time.Hour), GrabFailed: true}, reasonUnaired},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := itemReason(tc.f, now); got != tc.want {
				t.Errorf("itemReason = %q, want %q", got, tc.want)
			}
		})
	}
}

// A null air date is not a future one. AniList's coverage thins out badly before
// ~2015 and skips episodes even for modern titles, so an item with no schedule
// is searchable -- the sweep's own reading -- and must never read as unaired.
func TestItemReasonTreatsNoAirDateAsSearchable(t *testing.T) {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	if got := itemReason(itemFacts{}, now); got != "" {
		t.Errorf("itemReason = %q, want none: an unscheduled item is searchable", got)
	}
}
