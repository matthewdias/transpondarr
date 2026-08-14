package domain_test

import (
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/domain"
)

// An hour count past the duration ceiling wraps int64 when multiplied out, and
// 3000000h wraps *negative*, which reads as <= 0 and disables the wait outright:
// the longest wait a user can ask for silently becomes none. Clamped, it holds.
func TestPinDelayClampsBothEnds(t *testing.T) {
	cases := []struct {
		hours int64
		want  time.Duration
	}{
		{-1, 0},
		{0, 0},
		{6, 6 * time.Hour},
		{domain.MaxPinDelayHours, domain.MaxPinDelayHours * time.Hour},
		{domain.MaxPinDelayHours + 1, domain.MaxPinDelayHours * time.Hour},
		{3000000, domain.MaxPinDelayHours * time.Hour},   // wraps negative unclamped
		{999999999, domain.MaxPinDelayHours * time.Hour}, // wraps positive-but-wrong
	}
	for _, c := range cases {
		if got := domain.PinDelay(c.hours); got != c.want {
			t.Errorf("PinDelay(%d) = %v, want %v", c.hours, got, c.want)
		}
	}
}

// Format is the discriminator: a single-episode OVA stays title-shaped, so
// nothing may derive the kind from an item count.
func TestKindFor(t *testing.T) {
	for _, tc := range []struct {
		format domain.Format
		want   domain.WantedKind
	}{
		{domain.FormatTV, domain.KindEpisode},
		{domain.FormatOVA, domain.KindEpisode},
		{domain.FormatONA, domain.KindEpisode},
		{domain.FormatSpecial, domain.KindEpisode},
		{domain.FormatMovie, domain.KindMovie},
		{domain.Format(""), domain.KindEpisode},
	} {
		if got := domain.KindFor(tc.format); got != tc.want {
			t.Errorf("KindFor(%q) = %q, want %q", tc.format, got, tc.want)
		}
	}
}
