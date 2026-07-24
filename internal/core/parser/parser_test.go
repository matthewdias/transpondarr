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

func TestParseScoringAxes(t *testing.T) {
	tests := []struct {
		title    string
		source   string
		subs     string
		multiSub bool
		codec    string
		version  int
		repack   bool
	}{
		// Modern airing-style release: bare WEB tag, HEVC, a v2.
		{title: "[ExampleSubs] Placeholder Saga - 05v2 (WEB 1080p HEVC)",
			source: "web", codec: "h265", version: 2},
		// WEB-DL spelled out, with MultiSub.
		{title: "[ExampleSubs] Placeholder Saga S2E07 [1080p WEB-DL AAC][MultiSub][5A357DEE]",
			source: "web", multiSub: true},
		// BD encode from an archival group.
		{title: "[Archivers] Placeholder Saga - 12 [BD 1080p x265 FLAC]",
			source: "bd", codec: "h265"},
		{title: "[Archivers] Placeholder Saga - 12 (BDRip 1920x1080 AVC)",
			source: "bd", codec: "h264"},
		// TV rip with a codec we deliberately do not classify.
		{title: "[OldRips] Placeholder Saga - 03 [HDTV 480p XviD]",
			source: "tv"},
		{title: "[FreshEncodes] Placeholder Saga - 02 [1080p][AV1]",
			codec: "av1"},
		// Scene-style dot naming: REPACK and PROPER both mean "the first copy was bad".
		{title: "Placeholder.Saga.S01E04.REPACK.1080p.WEB.x264-FAKEGRP",
			source: "web", codec: "h264", repack: true},
		{title: "Placeholder.Saga.S01E10.PROPER.720p.HDTV.x264-FAKEGRP",
			source: "tv", codec: "h264", repack: true},
		// Sub-type markers.
		{title: "[SubCorp] Placeholder Saga - 09 [1080p][Hardsub]",
			subs: "hardsub"},
		{title: "[SubCorp] Placeholder Saga - 09 [1080p][Softsubs]",
			subs: "softsub"},
		{title: "[SubCorp] Placeholder Saga - 09 [1080p][Multi-Sub]",
			multiSub: true},
		// A series title containing "Web" must not read as Source=web.
		{title: "[SpiderGroup] Ghost Web - 03 [1080p]"},
		// Plain release with no axis markers at all: everything stays zero.
		{title: "[FakeGroup] Placeholder Saga - 28 (1080p) [ABCD1234].mkv"},
	}
	for _, tt := range tests {
		t.Run(tt.title, func(t *testing.T) {
			p := Parse(tt.title)
			if p.Source != tt.source {
				t.Errorf("source = %q, want %q", p.Source, tt.source)
			}
			if p.Subs != tt.subs {
				t.Errorf("subs = %q, want %q", p.Subs, tt.subs)
			}
			if p.MultiSub != tt.multiSub {
				t.Errorf("multiSub = %v, want %v", p.MultiSub, tt.multiSub)
			}
			if p.Codec != tt.codec {
				t.Errorf("codec = %q, want %q", p.Codec, tt.codec)
			}
			if p.Version != tt.version {
				t.Errorf("version = %d, want %d", p.Version, tt.version)
			}
			if p.Repack != tt.repack {
				t.Errorf("repack = %v, want %v", p.Repack, tt.repack)
			}
		})
	}
}
