package server

import (
	"time"

	"github.com/matthewdias/transpondarr/internal/core/acquire"
	"github.com/matthewdias/transpondarr/internal/core/settings"
)

// Why an item is still missing, split by scope (#150): one global answer for
// the page, one per title group, one per item. The tiers render together
// rather than competing for one slot, which is what let a failed grab and its
// blocklist entry both be visible.
//
// Every reason but the item's pass tier is derived from stored state at request
// time and so is never stale. The pass tier is the one stored answer (#181):
// what the last search or poll decided about this item, which is why it is
// always rendered with its own date.
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

	reasonNoMatch   = "no_match"
	reasonDeclined  = "declined"
	reasonPinHeld   = "pin_held"
	reasonWouldGrab = "would_grab"
	reasonAddFailed = "add_failed"
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

// titleFacts is one title's standing in the sweep queue, the scope a group
// header answers for.
type titleFacts struct {
	Monitored       bool
	BlockedReleases int
	LastSearchedAt  time.Time // zero when the title has never been searched
	NextSearchAt    time.Time // zero when the title is due now
}

// titleReason picks the fact that most explains the group, widest-first: what
// stops the title being a target, then its failure memory, then its queue slot.
func titleReason(f titleFacts, now time.Time) string {
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

// passFacts is what the last pass decided about this item (#181), or the zero
// value when no pass has spoken.
type passFacts struct {
	Outcome    string
	Release    string
	Detail     string
	Source     string
	RecordedAt time.Time
	HeldUntil  time.Time
}

// itemFacts is the state that varies row to row; a zero AirsAt is a provider
// gap, which the sweep reads as searchable, never as a date in the future.
type itemFacts struct {
	Monitored  bool
	AirsAt     time.Time
	GrabFailed bool
	GrabbedAt  time.Time // when the grab that failed was made
	Pass       passFacts
}

// Stored outcomes and surfaced reasons differ deliberately: acquire records
// seven, a row shows five. grabbed exists only as the tombstone that
// invalidates an older refusal -- a listed item's grab plainly did not hold,
// and grab_failed owns that row. contended is silent too, because its honest
// message is "the queue is working", which the group tier already says.
func passReason(outcome string) string {
	switch outcome {
	case acquire.OutcomeNoMatch:
		return reasonNoMatch
	case acquire.OutcomeDeclined:
		return reasonDeclined
	case acquire.OutcomePinHeld:
		return reasonPinHeld
	case acquire.OutcomeWouldGrab:
		return reasonWouldGrab
	case acquire.OutcomeAddFailed:
		return reasonAddFailed
	}
	return ""
}

// itemReason is the row's own story, or "" when the group and page tell it all;
// fromPass reports whether the stored tier won, which is what may carry an "as
// of" date.
//
// A pass answer outranks a failed grab because the two differ in kind. A
// failure is a handled past event -- the item reverted to wanted, a blocklist
// entry was written, and the group already reads "Releases blocklisted (N)" --
// while a decline is a standing, unresolved, user-actionable condition that
// appears nowhere else on the page and repeats silently every pass.
//
// Unmonitored is widest of all and suppresses everything below it: nothing about
// the row will be revisited while monitoring is off.
func itemReason(f itemFacts, now time.Time) (reason string, fromPass bool) {
	if !f.Monitored {
		return reasonUnmonitored, false
	}
	if !f.AirsAt.IsZero() && f.AirsAt.After(now) {
		return reasonUnaired, false
	}
	if r := passReason(f.currentOutcome()); r != "" {
		return r, true
	}
	if f.GrabFailed {
		return reasonGrabFailed, false
	}
	return "", false
}

// currentOutcome drops a pass answer older than the grab beside it. This is
// exactly equivalent to ranking on recency, with no timestamp arithmetic on the
// live path: a pass only writes for a grabbable item, and an item is not
// grabbable while its grab is live, so an outcome can never be recorded between
// a grab being made and failing. Monitoring only narrows that write set.
func (f itemFacts) currentOutcome() string {
	if f.GrabFailed && !f.GrabbedAt.IsZero() && !f.Pass.RecordedAt.After(f.GrabbedAt) {
		return ""
	}
	return f.Pass.Outcome
}
