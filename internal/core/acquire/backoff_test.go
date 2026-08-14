package acquire

import (
	"testing"
	"time"
)

// The schedule doubles from an hour and stops at a day, so a dead title is
// still re-checked daily rather than drifting into never.
func TestBackoffDelay(t *testing.T) {
	for _, tc := range []struct {
		n    int
		want time.Duration
	}{
		{0, time.Hour},
		{1, time.Hour},
		{2, 2 * time.Hour},
		{3, 4 * time.Hour},
		{4, 8 * time.Hour},
		{5, 16 * time.Hour},
		{6, 24 * time.Hour},
		{20, 24 * time.Hour},
	} {
		if got := backoffDelay(tc.n); got != tc.want {
			t.Errorf("backoffDelay(%d) = %v, want %v", tc.n, got, tc.want)
		}
	}
}
