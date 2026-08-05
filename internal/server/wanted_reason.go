package server

import (
	"time"

	"github.com/matthewdias/transpondarr/internal/core/settings"
)

// Why an item is still missing, derived from stored state at request time and
// split by scope (#150): one global answer for the page, one per series group,
// one per item. The tiers render together rather than competing for one slot,
// which is what let a failed grab and its blocklist entry both be visible.
// Refusals from a pass live only in memory for its length, so "the closest
// release was turned down" is deliberately not here (#181); everything below is
// re-read on every request and so is never stale.
const (
	reasonNoIndexer     = "no_indexer"
	reasonAutomationOff = "automation_off"
	reasonNotifyOnly    = "notify_only"

	reasonUnmonitored   = "unmonitored"
	reasonBlocklisted   = "blocklisted"
	reasonNeverSearched = "never_searched"
	reasonSearchBackoff = "search_backoff"
	reasonSearchDue     = "search_due"

	reasonUnaired    = "unaired"
	reasonGrabFailed = "grab_failed"
)

// globalReason is what stops any search running at all, or "" when nothing does.
func globalReason(indexerReady bool, mode settings.AutomationMode) string {
	switch {
	case !indexerReady:
		return reasonNoIndexer
	case mode == settings.AutomationOff:
		return reasonAutomationOff
	case mode == settings.AutomationNotifyOnly:
		return reasonNotifyOnly
	}
	return ""
}

// seriesFacts is one series' standing in the sweep queue, the scope a group
// header answers for.
type seriesFacts struct {
	Monitored       bool
	BlockedReleases int
	LastSearchedAt  time.Time // zero when the series has never been searched
	NextSearchAt    time.Time // zero when the series is due now
}

// seriesReason picks the fact that most explains the group, widest-first: what
// stops the series being a target, then its failure memory, then its queue slot.
func seriesReason(f seriesFacts, now time.Time) string {
	switch {
	case !f.Monitored:
		return reasonUnmonitored
	case f.BlockedReleases > 0:
		return reasonBlocklisted
	case f.LastSearchedAt.IsZero():
		return reasonNeverSearched
	case f.NextSearchAt.After(now):
		return reasonSearchBackoff
	}
	return reasonSearchDue
}

// itemFacts is the state that varies row to row; a zero AirsAt is a provider
// gap, which the sweep reads as searchable, never as a date in the future.
type itemFacts struct {
	AirsAt     time.Time
	GrabFailed bool
}

// itemReason is the row's own story, or "" when the group and page tell it all.
func itemReason(f itemFacts, now time.Time) string {
	switch {
	case !f.AirsAt.IsZero() && f.AirsAt.After(now):
		return reasonUnaired
	case f.GrabFailed:
		return reasonGrabFailed
	}
	return ""
}
