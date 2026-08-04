package acquire

import "testing"

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
