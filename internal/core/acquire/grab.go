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

// errItemsClaimed means another grab already has these items in flight. Only
// automation ever sees it, and only as "skip this candidate".
var errItemsClaimed = errors.New("acquire: items already claimed by a grab in flight")

// AutoGrab is Grab on automation's behalf: it also remembers a release the client
// could not resolve, the one failure path #118 could not reach since a refused
// add writes no grab row (#120). Only the release's own faults — a sick client
// says nothing about which release was asked for. Eligibility stays with the caller.
//
// It claims the covered items for the whole read→add→write window, so the sweep
// and the feed poll cannot both hand the same item to the download client.
func (s *Service) AutoGrab(ctx context.Context, seriesID int64, cand decide.Candidate, items []domain.WantedItem) (GrabResult, error) {
	ids := coveredItemIDs(cand, items)
	if !s.claims.TryAcquire(ids) {
		return GrabResult{}, errItemsClaimed
	}
	defer s.claims.Release(ids)

	res, err := s.Grab(ctx, cand, items, false)
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
// to gate one.
func (s *Service) Grab(ctx context.Context, cand decide.Candidate, items []domain.WantedItem, paused bool) (GrabResult, error) {
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
	for _, it := range items {
		itemID[it.Number] = it.ID
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
		// have stays false — only a successful import marks an item as had.
		if _, err := q.UpsertGrab(ctx, db.UpsertGrabParams{
			WantedItemID: id,
			InfoHash:     res.Hash,
			ReleaseTitle: cand.Release.Title,
			Status:       statusGrabbed,
		}); err != nil {
			return GrabResult{}, fmt.Errorf("record grab for item %d: %w", n, err)
		}
		grabbed = append(grabbed, n)
	}
	if err := tx.Commit(); err != nil {
		return GrabResult{}, fmt.Errorf("commit grabs: %w", err)
	}

	return GrabResult{InfoHash: res.Hash, Outcome: res.Outcome, Items: grabbed}, nil
}
