package server_test

import (
	"net/http"
	"testing"
)

// The three list DTOs a movie reaches without carrying its format (#215). Each
// is a word away from calling a film an episode, and format is the only thing
// that can stop it: item count cannot, because a one-episode OVA is a series
// (#208). Every one of these rides a join to series that already existed, so
// this asserts the column reaches the wire, not that a query grew.
func TestMissingGroupCarriesTheTitleFormat(t *testing.T) {
	h := wantedHarness(t)
	movieID := seedMovie(t, h.store, "Placeholder Film", 2019)
	seriesID := seedTitle(t, h.store, "Placeholder Saga", 2)

	var out missingResponse
	if code := h.get(t, "/api/v1/wanted/missing", &out); code != http.StatusOK {
		t.Fatalf("GET missing = %d, want 200", code)
	}
	got := map[int64]string{}
	for _, g := range out.Groups {
		got[g.TitleID] = g.Format
	}
	if got[movieID] != "MOVIE" {
		t.Errorf("movie group format = %q, want MOVIE", got[movieID])
	}
	if got[seriesID] != "TV" {
		t.Errorf("series group format = %q, want TV", got[seriesID])
	}
}

func TestQueueItemCarriesTheTitleFormat(t *testing.T) {
	h := newHarness(t, nil, nil)
	movieID := seedMovie(t, h.store, "Placeholder Film", 2019)
	seedOpenGrab(t, h.store, movieID, 1, "aaaa",
		"[SynthSubs] Placeholder Film (2019) [1080p]", "grabbed")

	var out queueJSON
	if code := h.get(t, "/api/v1/activity/queue", &out); code != http.StatusOK {
		t.Fatalf("GET queue = %d, want 200", code)
	}
	if len(out.Items) != 1 {
		t.Fatalf("queue items = %+v, want the one open grab", out.Items)
	}
	if out.Items[0].Format != "MOVIE" {
		t.Errorf("queue item format = %q, want MOVIE", out.Items[0].Format)
	}
}
