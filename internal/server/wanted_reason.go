package server

import (
	"time"

	"github.com/matthewdias/transpondarr/internal/core/settings"
)

// Why an item is still missing, derived from stored state at request time (#150).
// Refusals from a pass live only in memory for its length, so the closest release
// and why it was turned down are deliberately not here; everything below is
// re-read on every request and so is never stale.
const (
	reasonUnaired       = "unaired"
	reasonUnmonitored   = "unmonitored"
	reasonNoIndexer     = "no_indexer"
	reasonAutomationOff = "automation_off"
	reasonNotifyOnly    = "notify_only"
	reasonGrabFailed    = "grab_failed"
	reasonBlocklisted   = "blocklisted"
	reasonNeverSearched = "never_searched"
	reasonSearchBackoff = "search_backoff"
	reasonSearchDue     = "search_due"
)

// missingFacts is what the reason is derived from: the item's own state, its
// series' place in the sweep queue, and the two global conditions that decide
// whether any of that queue will move.
type missingFacts struct {
	AirsAt          time.Time // zero when the provider publishes no schedule
	Monitored       bool
	GrabFailed      bool
	BlockedReleases int
	LastSearchedAt  time.Time // zero when the series has never been searched
	NextSearchAt    time.Time // zero when the series is due now
	IndexerReady    bool
	Automation      settings.AutomationMode
}

// missingReason picks the one fact that most explains the row. The order is
// widest-first: what stops the item being a target at all, then what stops any
// search running, then this item's own last attempt, then where its series sits
// in the queue.
func missingReason(f missingFacts, now time.Time) string {
	switch {
	case !f.AirsAt.IsZero() && f.AirsAt.After(now):
		return reasonUnaired
	case !f.Monitored:
		return reasonUnmonitored
	case !f.IndexerReady:
		return reasonNoIndexer
	case f.Automation == settings.AutomationOff:
		return reasonAutomationOff
	case f.Automation == settings.AutomationNotifyOnly:
		return reasonNotifyOnly
	case f.GrabFailed:
		return reasonGrabFailed
	case f.BlockedReleases > 0:
		return reasonBlocklisted
	case f.LastSearchedAt.IsZero():
		return reasonNeverSearched
	case f.NextSearchAt.After(now):
		return reasonSearchBackoff
	}
	return reasonSearchDue
}
