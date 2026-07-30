package decide

import (
	"slices"
	"testing"
)

// All fixtures are invented titles; only the typography under test is real.

func TestSearchTerm(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"plain ascii unchanged", "Placeholder Saga", "Placeholder Saga"},
		{"multiplication sign to x, parens dropped", "RANGER×RANGER (2013)", "RANGER x RANGER 2013"},
		{"katakana middle dot to space", "Sora・no・Fixture", "Sora no Fixture"},
		{"white star to space", "Mahou☆Placeholder", "Mahou Placeholder"},
		{"eighth note to space", "Melody♪Fixture", "Melody Fixture"},
		{"fullwidth tilde pair to space", "Fixture Encore ～Reprise～", "Fixture Encore Reprise"},
		{"wave dash pair to space", "Fixture Encore 〜Reprise〜", "Fixture Encore Reprise"},
		{"vulgar half dropped", "Placeholder½ Saga", "Placeholder Saga"},
		{"brackets dropped", "Placeholder Saga [TV]", "Placeholder Saga TV"},
		{"whitespace collapsed", "Placeholder   Saga", "Placeholder Saga"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SearchTerm(tc.in); got != tc.want {
				t.Errorf("SearchTerm(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestSearchTermsOrderAndDedupe(t *testing.T) {
	got := SearchTerms("RANGER×RANGER (2013)", []string{
		"RANGER×RANGER (2013)",   // stored title repeated in variants
		"Ranger x Ranger (2013)", // english: sanitizes to a case-insensitive dupe
		"レンジャー×レンジャー",            // native: distinct, kept as the last resort
		"",
	})
	want := []string{"RANGER x RANGER 2013", "レンジャー x レンジャー"}
	if !slices.Equal(got, want) {
		t.Errorf("SearchTerms = %q, want %q", got, want)
	}
}
