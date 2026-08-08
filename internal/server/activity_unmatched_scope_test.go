package server

import (
	"testing"

	"github.com/matthewdias/transpondarr/internal/core/download"
)

// The category is the entire safety boundary, so a blank one keeps nothing:
// without it our torrents are indistinguishable from the user's, and the
// endpoint reports scoped: false rather than guessing. In-package because
// settings.applyDefaults means a live service can never hand out a blank one.
func TestPickUnmatchedKeepsNothingWithoutACategory(t *testing.T) {
	statuses := []download.Status{
		{Hash: "aaaa1111", Category: ""},
		{Hash: "bbbb2222", Category: "transpondarr"},
	}
	if got := pickUnmatched(statuses, nil, ""); len(got) != 0 {
		t.Errorf("pickUnmatched with no category = %+v, want nothing", got)
	}
	if got := pickUnmatched(statuses, nil, "   "); len(got) != 0 {
		t.Errorf("pickUnmatched with a blank category = %+v, want nothing", got)
	}
	got := pickUnmatched(statuses, map[string]bool{}, "transpondarr")
	if len(got) != 1 || got[0].Hash != "bbbb2222" {
		t.Errorf("pickUnmatched = %+v, want only the torrent in our category", got)
	}
}
