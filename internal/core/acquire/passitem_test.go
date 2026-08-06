package acquire

import "testing"

// A held item reaches the matcher as a candidate carrying what holds it: the
// case the InLibrary/grabbable split was made for (#97).
func TestPassItemsCarriesHeldIdentityToTheMatcher(t *testing.T) {
	const held = "[ExampleSubs] Placeholder Saga - 02 [480p]"
	got := matchItems(passItems([]sweepItem{
		{number: 1, inLibrary: false, grabbable: true},
		{number: 2, inLibrary: true, grabbable: true, heldTitle: held},
		{number: 3, inLibrary: true, grabbable: false, heldTitle: held}, // withheld: candidacy is grabbable's job
	}))
	want := []string{"", held, held}
	for i, w := range want {
		if got[i].HeldTitle != w {
			t.Errorf("item %d: HeldTitle = %q, want %q", got[i].Number, got[i].HeldTitle, w)
		}
	}
}

// The contract the split exists for: grabbable is the pass's answer, InLibrary
// is the library's. Nothing downstream reads InLibrary off a sweep, so a
// regression to the old InLibrary: !grabbable would compile and stay invisible
// until #97 asked.
func TestPassItemsReportsPossessionAndCandidacySeparately(t *testing.T) {
	got := passItems([]sweepItem{
		{number: 1, inLibrary: false, grabbable: false}, // in flight or unaired: withheld, not held
		{number: 2, inLibrary: true, grabbable: false},  // imported: withheld and held
		{number: 3, inLibrary: false, grabbable: true},  // wanted
	})
	want := []struct{ inLibrary, grabbable bool }{{false, false}, {true, false}, {false, true}}
	for i, w := range want {
		if got[i].InLibrary != w.inLibrary || got[i].grabbable != w.grabbable {
			t.Errorf("item %d: InLibrary=%v grabbable=%v, want InLibrary=%v grabbable=%v",
				got[i].Number, got[i].InLibrary, got[i].grabbable, w.inLibrary, w.grabbable)
		}
	}
}
