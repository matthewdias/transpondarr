package parser

import "testing"

// All fixtures are invented film and group names; only the naming structure is real.

// The parser reports a year only when the release *isolates* one in brackets —
// anitogo's rule, pinned here so a dependency bump that changes it fails loudly
// rather than silently widening what decide's year gate compares.
func TestParseYear(t *testing.T) {
	tests := []struct {
		name  string
		title string
		want  int
	}{
		{"parenthesised", "[ExampleSubs] Sample Film (2019) [1080p][HEVC]", 2019},
		{"bracketed", "[ExampleSubs] Sample Film [2019] [1080p][HEVC]", 2019},
		{"after a dashed subtitle", "[ExampleSubs] Sample Film - The Motion Picture (2019) [1080p]", 2019},
		// The scene form glues the year into the anime title, so nothing is
		// isolated and the parser reports none. decide recovers it against the
		// title variants, which is the only place the ambiguity can be resolved.
		{"scene dot form reports none", "Sample.Film.2019.2160p.WEB-DL.H.264-EXGRP", 0},
		{"bare token reports none", "[ExampleSubs] Sample Film 2019 [1080p][HEVC]", 0},
		{"out of range is not a year", "[ExampleSubs] Sample Film 2199 [BD 1080p]", 0},
		{"no year at all", "[ExampleSubs] Sample Film [BD 1080p]", 0},
		{"episodic release", "[ExampleSubs] Sample Show - 01 [1080p]", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Parse(tt.title).Year; got != tt.want {
				t.Errorf("year = %d, want %d", got, tt.want)
			}
		})
	}
}

// A dual-titled release still yields the year, and the title keeps its own
// trailing number: both readings of a four-digit token can appear in one name.
func TestParseYearAlongsideATitleNumber(t *testing.T) {
	p := Parse("[ExampleSubs] Sample Film 2019 (2021) [1080p]")
	if p.Year != 2021 {
		t.Errorf("year = %d, want 2021", p.Year)
	}
	if p.Title != "Sample Film 2019" {
		t.Errorf("title = %q, want the title's own number kept", p.Title)
	}
}

// A numbered sequel film reads as an episode number, which is why movie mode
// ignores episode numbers entirely rather than trusting them.
func TestParseNumberedSequelFilmReadsAsAnEpisode(t *testing.T) {
	p := Parse("[ExampleSubs] Sample Film 2 (2021) [1080p]")
	if p.EpisodeStart != 2 {
		t.Errorf("episodeStart = %d, want the sequel number misread as 2", p.EpisodeStart)
	}
	if p.Year != 2021 {
		t.Errorf("year = %d, want 2021", p.Year)
	}
}
