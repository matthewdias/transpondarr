package parser

import "testing"

// All fixtures use invented series and group names; only the structural naming
// conventions (season-relative vs absolute numbering, batch ranges, dual-audio,
// resolution/group tags) are what the parser is exercised against.

func TestParseSeasonRelativeEpisode(t *testing.T) {
	p := Parse("[ExampleSubs] Placeholder Saga S2E07 [1080p WEB-DL AAC][MultiSub][5A357DEE]")
	if p.Title != "Placeholder Saga" {
		t.Errorf("title = %q, want Placeholder Saga", p.Title)
	}
	if p.Season != 2 {
		t.Errorf("season = %d, want 2", p.Season)
	}
	if p.EpisodeStart != 7 || p.EpisodeEnd != 7 {
		t.Errorf("episode = %d..%d, want 7..7", p.EpisodeStart, p.EpisodeEnd)
	}
	if p.Batch {
		t.Error("single episode should not be a batch")
	}
	if p.Group != "ExampleSubs" {
		t.Errorf("group = %q, want ExampleSubs", p.Group)
	}
	if p.Resolution != "1080p" {
		t.Errorf("resolution = %q, want 1080p", p.Resolution)
	}
}

func TestParseAbsoluteEpisode(t *testing.T) {
	p := Parse("[FakeGroup] Placeholder Saga - 28 (1080p) [ABCD1234].mkv")
	if p.Title != "Placeholder Saga" {
		t.Errorf("title = %q, want Placeholder Saga", p.Title)
	}
	if p.EpisodeStart != 28 || p.EpisodeEnd != 28 {
		t.Errorf("episode = %d..%d, want 28..28", p.EpisodeStart, p.EpisodeEnd)
	}
	if p.Season != 0 {
		t.Errorf("season = %d, want 0 (unspecified)", p.Season)
	}
	if p.Batch {
		t.Error("single absolute episode should not be a batch")
	}
}

func TestParseBatchRange(t *testing.T) {
	p := Parse("[Batchers] Placeholder Saga S2 (01-10) (1080p) [Batch]")
	if p.EpisodeStart != 1 || p.EpisodeEnd != 10 {
		t.Errorf("episode = %d..%d, want 1..10", p.EpisodeStart, p.EpisodeEnd)
	}
	if !p.Batch {
		t.Error("an episode range should be a batch")
	}
	if p.Season != 2 {
		t.Errorf("season = %d, want 2", p.Season)
	}
}

func TestParseSeasonPackNoEpisodeNumber(t *testing.T) {
	p := Parse("[DualCorp] Placeholder Saga (S01) [BD 1080p][Dual-Audio] (Batch)")
	if p.EpisodeStart != 0 {
		t.Errorf("episode start = %d, want 0 (no single number)", p.EpisodeStart)
	}
	if !p.Batch {
		t.Error("a season pack with a Batch marker should be a batch")
	}
	if !p.DualAudio {
		t.Error("Dual-Audio should be detected")
	}
}

func TestParseDualAudioFromAudioTerm(t *testing.T) {
	p := Parse("[Group] Placeholder Saga S1E01 [1080p][HEVC][Dual Audio]")
	if !p.DualAudio {
		t.Error("Dual Audio should be detected")
	}
}

func TestParseNonDualAudio(t *testing.T) {
	p := Parse("[Group] Placeholder Saga S1E01 [1080p][AAC][MultiSub]")
	if p.DualAudio {
		t.Error("MultiSub/AAC release should not be flagged dual-audio")
	}
}
