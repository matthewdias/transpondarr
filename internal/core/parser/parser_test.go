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

// anitogo reports whatever form the release name used, so a dimension-form title
// must fold to the height form the quality profile axes are written in.
func TestParseResolutionNormalization(t *testing.T) {
	tests := []struct {
		title string
		res   string
		raw   string
	}{
		{title: "[Archivers] Placeholder Saga - 12 (BDRip 1920x1080 AVC)", res: "1080p", raw: "1920x1080"},
		{title: "[Archivers] Placeholder Saga - 12 [BD 1280x720 x264]", res: "720p", raw: "1280x720"},
		{title: "[UHDGroup] Placeholder Saga - 01 [3840x2160 HEVC]", res: "2160p", raw: "3840x2160"},
		{title: "[OldRips] Placeholder Saga - 03 [640x480 XviD]", res: "480p", raw: "640x480"},
		// A cropped/anamorphic BD encode is still a 1080p release.
		{title: "[Archivers] Placeholder Saga - 12 [BD 1920x1036 FLAC]", res: "1080p", raw: "1920x1036"},
		// The raw form keeps the release's own casing, so it reads back against the title.
		{title: "[Archivers] Placeholder Saga - 12 [BD 1920X1080]", res: "1080p", raw: "1920X1080"},
		{title: "[Archivers] Placeholder Saga - 12 [BD 1920×1080]", res: "1080p", raw: "1920×1080"},
		// Neither dimension names a tier: the height is the honest answer.
		{title: "[OldRips] Placeholder Saga - 03 [960x544 XviD]", res: "544p", raw: "960x544"},
		// A non-tier height with a tier-naming width folds by the width table.
		{title: "[OldRips] Placeholder Saga - 03 [704x396 XviD]", res: "480p", raw: "704x396"},
		// anitogo extracts a glued standard tier itself; a glued non-tier height
		// reaches us whole and folds to its suffix.
		{title: "[FakeGroup] Placeholder Saga - 05 [BD1080p]", res: "1080p"},
		{title: "[FakeGroup] Placeholder Saga - 05 [BD540p]", res: "540p", raw: "BD540p"},
		// anitogo classes 4K as a video term, so it names the tier only when no
		// digit form did.
		{title: "[UHDGroup] Placeholder Saga - 01 [4K HEVC]", res: "2160p", raw: "4K"},
		// anitogo's resolution pattern is unanchored, so junk can reach us carrying a
		// separator (both found by the fuzzer); it must resolve to nothing rather
		// than escape with the separator intact.
		{title: "000X000"},
		{title: "A000X000"},
		// Already canonical: nothing was inferred, so no raw form is reported.
		{title: "[ExampleSubs] Placeholder Saga - 05 [1080p]", res: "1080p"},
		{title: "[ExampleSubs] Placeholder Saga - 05 [1080P]", res: "1080p"},
	}
	for _, tt := range tests {
		t.Run(tt.title, func(t *testing.T) {
			p := Parse(tt.title)
			if p.Resolution != tt.res {
				t.Errorf("resolution = %q, want %q", p.Resolution, tt.res)
			}
			if p.ResolutionRaw != tt.raw {
				t.Errorf("resolutionRaw = %q, want %q", p.ResolutionRaw, tt.raw)
			}
		})
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
		// The same guard must hold for scene dot-names, where anitogo's parsed
		// title ("Ghost Web") no longer matches the raw text ("Ghost.Web").
		{title: "Ghost.Web.S01E03.1080p.x264-FAKEGRP",
			codec: "h264"},
		// An episode title containing "Web" must not read as Source=web either.
		{title: "[SubCorp] Placeholder Saga - 05 - The Web of Fate [1080p]"},
		// A dual-title scene release with no episode title: anitogo misfiles the
		// tag run as EpisodeTitle, which must not blank the axes it carries.
		{title: "Phantom Courier S01E02 1080p NF WEB-DL MULTi AAC2.0 x265-FAKEGRP (Romaji Title, Multi-Audio, Multi-Subs)",
			source: "web", codec: "h265", multiSub: true},
		// The same shape without a codec token: WEB-DL alone marks the tag run.
		{title: "Phantom Courier S01E02 1080p NF WEB-DL MULTi AAC2.0-FAKEGRP (Romaji Title, Multi-Audio, Multi-Subs)",
			source: "web", multiSub: true},
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
