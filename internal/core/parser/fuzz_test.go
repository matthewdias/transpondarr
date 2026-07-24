package parser

import "testing"

// Release titles arrive from external Torznab indexers and anitogo is
// unmaintained, so guard that Parse never panics on arbitrary input.
func FuzzParseNoPanic(f *testing.F) {
	seeds := []string{
		"",
		"[ExampleSubs] Placeholder Saga S2E07 [1080p WEB-DL AAC][MultiSub][5A357DEE]",
		"[FakeGroup] Placeholder Saga - 28 (1080p) [ABCD1234].mkv",
		"[Batchers] Placeholder Saga S02E13 (01-10) (1080p) [Batch]",
		"Placeholder Saga S3 - 01 (51) [Dual Audio]",
		"[[]]()---..__~~!!##$$%%^^&&**(())",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, title string) {
		p := Parse(title)
		if p.EpisodeEnd < p.EpisodeStart {
			t.Errorf("Parse(%q): EpisodeEnd %d < EpisodeStart %d", title, p.EpisodeEnd, p.EpisodeStart)
		}
		if p.Season < 0 || p.EpisodeStart < 0 || p.EpisodeEnd < 0 || p.AbsoluteEpisode < 0 {
			t.Errorf("Parse(%q): negative numeric field: %+v", title, p)
		}
		if p.EpisodeEnd > p.EpisodeStart && !p.Batch {
			t.Errorf("Parse(%q): episode range %d-%d but Batch is false", title, p.EpisodeStart, p.EpisodeEnd)
		}
	})
}
