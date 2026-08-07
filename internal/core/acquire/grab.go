package acquire

import (
	"context"
	"errors"
	"fmt"

	"github.com/matthewdias/transpondarr/internal/core/decide"
	"github.com/matthewdias/transpondarr/internal/core/domain"
	"github.com/matthewdias/transpondarr/internal/core/download"
	"github.com/matthewdias/transpondarr/internal/store/db"
)

// statusGrabbed is the grab lifecycle's only unsettled state (see the importer).
const statusGrabbed = "grabbed"

// GrabResult reports what the download client did and which item numbers were
// recorded against the resulting info hash.
type GrabResult struct {
	InfoHash string
	Outcome  download.AddOutcome
	Items    []int
}

// errItemsTaken means these items are no longer this pass's to grab. Only
// automation ever sees it, and only as "skip this candidate".
var errItemsTaken = errors.New("acquire: items taken by another grab")

// AutoGrab is Grab on automation's behalf: it also remembers a release the client
// could not resolve, the one failure path #118 could not reach since a refused
// add writes no grab row (#120). Only the release's own faults — a sick client
// says nothing about which release was asked for. Eligibility stays with the caller.
//
// The claim alone would not be enough. A caller reads its item list, spends
// seconds out on the network, and grabs afterwards, so the other entry point can
// take an item and release its claim entirely within that gap — leaving a stale
// read that passes TryAcquire. Re-reading grab state under the claim is what
// closes it, and it is race-free precisely because the claim serializes writers:
// any automation grab that committed did so before releasing, and so before this
// one acquired.
func (s *Service) AutoGrab(ctx context.Context, seriesID int64, cand decide.Candidate, items []domain.WantedItem) (GrabResult, error) {
	// Automation acts on the take set alone: a grab row is written per covered
	// item, so grabbing on Items would re-open the held items the upgrade policy
	// just refused.
	upgrades := itemIDSet(cand.UpgradeItems, items)
	cand.Items = cand.TakeItems()

	ids := coveredItemIDs(cand, items)
	if !s.claims.TryAcquire(ids) {
		return GrabResult{}, fmt.Errorf("%w: a grab is in flight", errItemsTaken)
	}
	defer s.claims.Release(ids)

	if settled, err := s.anySettled(ctx, seriesID, ids, upgrades); err != nil {
		return GrabResult{}, err
	} else if settled {
		return GrabResult{}, fmt.Errorf("%w: settled since the pass read them", errItemsTaken)
	}

	res, err := s.Grab(ctx, seriesID, cand, items, false)
	if err == nil || !errors.Is(err, download.ErrBadRelease) || s.blocklist == nil {
		return res, err
	}
	if _, rerr := s.blocklist.Record(ctx, seriesID, ids,
		cand.Release.InfoHash, cand.Release.Title,
		"the download URL could not be fetched or parsed"); rerr != nil {
		s.log.Error("acquire: record blocklist entry for a refused add",
			"series", seriesID, "release", cand.Release.Title, "err", rerr)
	}
	return res, err
}

// anySettled reports whether any of ids already carries a grab this pass may not
// take. Settled is every status but failed, matching what loadSweepItems calls
// ungrabbable — one definition, so a re-check cannot disagree with the read it
// is guarding — with the one exception an upgrade is: an imported grab is
// exactly what an approved upgrade replaces.
func (s *Service) anySettled(ctx context.Context, seriesID int64, ids []int64, upgrades map[int64]bool) (bool, error) {
	grabs, err := s.store.Q.ListGrabsBySeries(ctx, seriesID)
	if err != nil {
		return false, fmt.Errorf("re-read grab state for series %d: %w", seriesID, err)
	}
	wanted := make(map[int64]bool, len(ids))
	for _, id := range ids {
		wanted[id] = true
	}
	for _, g := range grabs {
		if !wanted[g.WantedItemID] || g.Status == statusFailed {
			continue
		}
		if g.Status == statusImported && upgrades[g.WantedItemID] {
			continue
		}
		return true, nil
	}
	return false, nil
}

// itemIDSet resolves item numbers to the ids a grab is keyed on.
func itemIDSet(numbers []int, items []domain.WantedItem) map[int64]bool {
	if len(numbers) == 0 {
		return nil
	}
	byNumber := make(map[int]int64, len(items))
	for _, it := range items {
		byNumber[it.Number] = it.ID
	}
	out := make(map[int64]bool, len(numbers))
	for _, n := range numbers {
		if id, ok := byNumber[n]; ok {
			out[id] = true
		}
	}
	return out
}

// coveredItemIDs resolves a candidate's item numbers to ids, the form the
// blocklist takes a failure's breadth in.
func coveredItemIDs(cand decide.Candidate, items []domain.WantedItem) []int64 {
	byNumber := make(map[int]int64, len(items))
	for _, it := range items {
		byNumber[it.Number] = it.ID
	}
	ids := make([]int64, 0, len(cand.Items))
	for _, n := range cand.Items {
		if id, ok := byNumber[n]; ok {
			ids = append(ids, id)
		}
	}
	return ids
}

// Grab hands a candidate to the download client and records a grab per covered
// item. It never consults eligibility: a manual grab is explicit user intent and
// must not be refused (PR #57), so enforcement belongs to the sweep, which
// checks Eligible before calling. It takes an unconditional claim for the same
// reason — the claim exists to make automation yield to a grab in flight, never
// to gate one. seriesID must be the series the items belong to — nothing
// cross-checks it, and history events are recorded under it.
func (s *Service) Grab(ctx context.Context, seriesID int64, cand decide.Candidate, items []domain.WantedItem, paused bool) (GrabResult, error) {
	dl := s.clients.Download()
	if dl == nil {
		return GrabResult{}, ErrNoDownloadClient
	}
	ids := coveredItemIDs(cand, items)
	s.claims.Acquire(ids)
	defer s.claims.Release(ids)

	// Outside the transaction: the client is a remote side effect that cannot be
	// rolled back, and holding a write tx across it would serialize on the network.
	res, err := dl.Add(ctx, download.AddOptions{
		URL:      cand.Release.DownloadURL,
		Category: s.cfg.DownloadCategory(),
		Paused:   paused,
	})
	if err != nil {
		return GrabResult{}, fmt.Errorf("%w: %w", ErrDownloadAdd, err)
	}

	itemID := make(map[int]int64, len(items))
	itemKind := make(map[int]domain.WantedKind, len(items))
	for _, it := range items {
		itemID[it.Number] = it.ID
		itemKind[it.Number] = it.Kind
	}

	tx, err := s.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return GrabResult{}, fmt.Errorf("begin grab tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful Commit
	q := s.store.Q.WithTx(tx)

	// One transaction for every covered item: a release that covers three episodes
	// must not leave one recorded and two lost to a mid-write failure.
	grabbed := make([]int, 0, len(cand.Items))
	for _, n := range cand.Items {
		id, ok := itemID[n]
		if !ok {
			continue
		}
		// in_library stays false — only a successful import puts a file there.
		if _, err := q.UpsertGrab(ctx, db.UpsertGrabParams{
			WantedItemID: id,
			InfoHash:     res.Hash,
			ReleaseTitle: cand.Release.Title,
			Status:       statusGrabbed,
		}); err != nil {
			return GrabResult{}, fmt.Errorf("record grab for item %d: %w", n, err)
		}
		// Same tx as the grab row: the history row and the state it explains are atomic.
		if err := q.AppendGrabEvent(ctx, db.AppendGrabEventParams{
			SeriesID:     seriesID,
			WantedItemID: id,
			ItemNumber:   int64(n),
			ItemKind:     string(itemKind[n]),
			InfoHash:     res.Hash,
			ReleaseTitle: cand.Release.Title,
			Event:        statusGrabbed,
		}); err != nil {
			return GrabResult{}, fmt.Errorf("record grab event for item %d: %w", n, err)
		}
		grabbed = append(grabbed, n)
	}
	if err := tx.Commit(); err != nil {
		return GrabResult{}, fmt.Errorf("commit grabs: %w", err)
	}

	return GrabResult{InfoHash: res.Hash, Outcome: res.Outcome, Items: grabbed}, nil
}
