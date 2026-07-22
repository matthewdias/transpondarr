package anilist

import (
	"testing"

	"github.com/matthewdias/transpondarr/internal/core/domain"
)

func TestMapFormat(t *testing.T) {
	cases := map[string]domain.Format{
		"TV":       domain.FormatTV,
		"TV_SHORT": domain.FormatTV,
		"OVA":      domain.FormatOVA,
		"ONA":      domain.FormatONA,
		"SPECIAL":  domain.FormatSpecial,
		"MOVIE":    domain.FormatMovie,
		"MUSIC":    domain.FormatTV, // unknown/unsupported falls back to TV
		"":         domain.FormatTV,
	}
	for in, want := range cases {
		if got := mapFormat(in); got != want {
			t.Errorf("mapFormat(%q) = %q, want %q", in, got, want)
		}
	}
}
