package server

import (
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/settings"
)

// The reason column is a ranking, not a set: a row states the one fact that most
// explains why it is still missing, so the ordering between them is the whole
// contract.
func TestMissingReasonRanking(t *testing.T) {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	// ready is a row with nothing wrong: monitored, aired, searched, due now.
	ready := func() missingFacts {
		return missingFacts{
			AirsAt:         now.Add(-24 * time.Hour),
			Monitored:      true,
			LastSearchedAt: now.Add(-time.Hour),
			IndexerReady:   true,
			Automation:     settings.AutomationOn,
		}
	}

	cases := []struct {
		name string
		with func(*missingFacts)
		want string
	}{
		{"due now", func(*missingFacts) {}, reasonSearchDue},
		{"backing off", func(f *missingFacts) { f.NextSearchAt = now.Add(2 * time.Hour) }, reasonSearchBackoff},
		{"never searched", func(f *missingFacts) { f.LastSearchedAt = time.Time{} }, reasonNeverSearched},
		{"blocklisted outranks the queue", func(f *missingFacts) {
			f.BlockedReleases = 3
			f.NextSearchAt = now.Add(2 * time.Hour)
		}, reasonBlocklisted},
		{"a failed grab outranks the blocklist it wrote", func(f *missingFacts) {
			f.GrabFailed, f.BlockedReleases = true, 3
		}, reasonGrabFailed},
		{"notify-only outranks the item's own failure", func(f *missingFacts) {
			f.GrabFailed = true
			f.Automation = settings.AutomationNotifyOnly
		}, reasonNotifyOnly},
		{"automation off outranks notify-only", func(f *missingFacts) {
			f.Automation = settings.AutomationOff
		}, reasonAutomationOff},
		{"no indexer outranks automation state", func(f *missingFacts) {
			f.IndexerReady = false
			f.Automation = settings.AutomationOff
		}, reasonNoIndexer},
		{"unmonitored outranks every global blocker", func(f *missingFacts) {
			f.Monitored = false
			f.IndexerReady = false
		}, reasonUnmonitored},
		{"unaired outranks being unmonitored", func(f *missingFacts) {
			f.AirsAt = now.Add(time.Hour)
			f.Monitored = false
		}, reasonUnaired},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := ready()
			tc.with(&f)
			if got := missingReason(f, now); got != tc.want {
				t.Errorf("missingReason = %q, want %q", got, tc.want)
			}
		})
	}
}

// A null air date is not a future one. AniList's coverage thins out badly before
// ~2015 and skips episodes even for modern titles, so an item with no schedule
// is searchable — the sweep's own reading — and must never read as unaired.
func TestMissingReasonTreatsNoAirDateAsSearchable(t *testing.T) {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	f := missingFacts{Monitored: true, IndexerReady: true, Automation: settings.AutomationOn}
	if got := missingReason(f, now); got != reasonNeverSearched {
		t.Errorf("missingReason = %q, want %q: an unscheduled item is searchable", got, reasonNeverSearched)
	}
}
