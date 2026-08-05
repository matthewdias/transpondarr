package acquire

import "testing"

// A held item reaches the matcher as a candidate carrying what holds it: the
// case the Have/grabbable split was made for (#97).
func TestPassItemsCarriesHeldIdentityToTheMatcher(t *testing.T) {
	const held = "[ExampleSubs] Placeholder Saga - 02 [480p]"
	got := matchItems(passItems([]sweepItem{
		{number: 1, had: false, grabbable: true},
		{number: 2, had: true, grabbable: true, heldTitle: held},
		{number: 3, had: true, grabbable: false, heldTitle: held}, // withheld: candidacy is grabbable's job
	}))
	want := []string{"", held, held}
	for i, w := range want {
		if got[i].HeldTitle != w {
			t.Errorf("item %d: HeldTitle = %q, want %q", got[i].Number, got[i].HeldTitle, w)
		}
	}
}

// The contract the split exists for: grabbable is the pass's answer, Have is the
// library's. Nothing downstream reads Have off a sweep, so a regression to the
// old Have: !grabbable would compile and stay invisible until #97 asked.
func TestPassItemsReportsPossessionAndCandidacySeparately(t *testing.T) {
	got := passItems([]sweepItem{
		{number: 1, had: false, grabbable: false}, // in flight or unaired: withheld, not held
		{number: 2, had: true, grabbable: false},  // imported: withheld and held
		{number: 3, had: false, grabbable: true},  // wanted
	})
	want := []struct{ have, grabbable bool }{{false, false}, {true, false}, {false, true}}
	for i, w := range want {
		if got[i].Have != w.have || got[i].grabbable != w.grabbable {
			t.Errorf("item %d: Have=%v grabbable=%v, want Have=%v grabbable=%v",
				got[i].Number, got[i].Have, got[i].grabbable, w.have, w.grabbable)
		}
	}
}
