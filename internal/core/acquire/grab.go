package acquire

import (
	"context"
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

// Grab hands a candidate to the download client and records a grab per covered
// item. It never consults eligibility: a manual grab is explicit user intent and
// must not be refused (PR #57), so enforcement belongs to the sweep, which
// checks Eligible before calling.
func (s *Service) Grab(ctx context.Context, cand decide.Candidate, items []domain.WantedItem, paused bool) (GrabResult, error) {
	dl := s.clients.Download()
	if dl == nil {
		return GrabResult{}, ErrNoDownloadClient
	}

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
