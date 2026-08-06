package acquire

import (
	"context"
	"database/sql"
	"log/slog"
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/clients"
	"github.com/matthewdias/transpondarr/internal/core/decide"
	"github.com/matthewdias/transpondarr/internal/core/domain"
	"github.com/matthewdias/transpondarr/internal/core/indexer"
	"github.com/matthewdias/transpondarr/internal/coretest"
	"github.com/matthewdias/transpondarr/internal/store/db"
)

// walkConfig rehearses, so the walk decides without a download client.
type walkConfig struct{ pinDelay time.Duration }

func (walkConfig) DownloadCategory() string         { return "transpondarr" }
func (walkConfig) AutomationEnabled() bool          { return true }
func (walkConfig) NotifyOnly() bool                 { return true }
func (c walkConfig) PinDelayDefault() time.Duration { return c.pinDelay }

func walkCandidate(title string, number int, eligible bool) decide.Candidate {
	c := decide.Candidate{
		Release: indexer.Release{Title: title, Seeders: 100},
		Matched: true, Items: []int{number}, Eligible: eligible,
	}
	if !eligible {
		c.IneligibleReason = "below the profile floor"
	}
	return c
}

// covered and the outcome set are maintained side by side rather than merged --
// covered runs per candidate on the feed's hot path, and leaving it untouched
// kept the existing suite an honest regression net over a restructured
// function. This is the invariant that pairing rests on: an item is covered
// exactly when a settling outcome closed it.
func TestCoveredAgreesWithTheSettlingOutcomes(t *testing.T) {
	st := coretest.NewStore(t)
	s := New(st, clients.New(), nil, walkConfig{pinDelay: 6 * time.Hour},
		slog.New(slog.DiscardHandler), nil)

	aired := time.Now().Add(-time.Hour)
	sweep := []sweepItem{
		{id: 1, kind: domain.KindEpisode, number: 1, airsAt: aired, grabbable: true},
		{id: 2, kind: domain.KindEpisode, number: 2, airsAt: aired, grabbable: true},
		{id: 3, kind: domain.KindEpisode, number: 3, airsAt: aired, grabbable: true},
	}
	m := Match{
		Items: []domain.WantedItem{
			{ID: 1, Kind: domain.KindEpisode, Number: 1},
			{ID: 2, Kind: domain.KindEpisode, Number: 2},
			{ID: 3, Kind: domain.KindEpisode, Number: 3},
		},
		Candidates: []decide.Candidate{
			// The pinned group's own release is taken; another group's waits;
			// the third is refused outright and reaches the tail.
			walkCandidate("[PinnedSubs] Placeholder Saga - 01", 1, true),
			walkCandidate("[OtherSubs] Placeholder Saga - 02", 2, true),
			walkCandidate("[OtherSubs] Placeholder Saga - 03", 3, false),
		},
	}
	m.Candidates[0].Pinned = true
	series := db.Series{ID: 1, Title: "Placeholder Saga",
		PinnedGroup: sql.NullString{String: "PinnedSubs", Valid: true}}

	res, err := s.walkCandidates(context.Background(), series, m, sweep, time.Now(), sourceSweep)
	if err != nil {
		t.Fatalf("walkCandidates: %v", err)
	}
	finalizeOutcomes(&res, m, sweep, sourceSweep)

	// Non-vacuous in both directions: one settled take, one settled hold, one
	// refusal the walk left uncovered.
	if len(res.outcomes) != 3 {
		t.Fatalf("outcomes = %+v, want one per item", res.outcomes)
	}
	for _, it := range sweep {
		o := res.outcomes[it.number]
		if got := settling(o.kind); got != res.covered[it.number] {
			t.Errorf("episode %d: covered=%t but outcome %q settles=%t",
				it.number, res.covered[it.number], o.kind, got)
		}
	}
}
