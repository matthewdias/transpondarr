// Package parser turns a raw release title into structured fields — title,
// season, episode number(s), release group, resolution, dual-audio — using
// anitogo (a Go port of Anitomy): anime filename conventions are a large
// heuristic problem better handled by a maintained library than by hand.
//
// The parser is deliberately dumb about *identity*: it extracts what the release
// name says, and never decides which series or wanted item it belongs to — that
// reconciliation is the decide layer's job.
package parser

import (
	"strconv"
	"strings"

	"github.com/nssteinbrenner/anitogo"
)

// Parsed is the structured view of a release title. Zero values mean "absent"
// (Season/EpisodeStart/EpisodeEnd == 0 when the title carried no such token).
type Parsed struct {
	Title      string // AnimeTitle, e.g. "Some Show"
	Group      string // release group, e.g. "ExampleSubs"
	Resolution string // e.g. "1080p"

	Season       int // 0 when unspecified
	EpisodeStart int // first episode number; 0 when none
	EpisodeEnd   int // last episode number; == EpisodeStart for a single episode

	// AbsoluteEpisode is anitogo's alternate number — the absolute count when the
	// title gives a season-relative number plus an absolute one (e.g. "S3 - 01 (51)").
	// 0 when absent.
	AbsoluteEpisode int

	Batch     bool // a multi-episode release (range, or a season pack with no number)
	DualAudio bool
}

// Parse extracts structured fields from a release title.
func Parse(title string) Parsed {
	e := anitogo.Parse(title, anitogo.DefaultOptions)

	start, end := episodeRange(e.EpisodeNumber)
	p := Parsed{
		Title:           strings.TrimSpace(e.AnimeTitle),
		Group:           e.ReleaseGroup,
		Resolution:      e.VideoResolution,
		Season:          firstInt(e.AnimeSeason),
		EpisodeStart:    start,
		EpisodeEnd:      end,
		AbsoluteEpisode: firstInt(e.EpisodeNumberAlt),
		DualAudio:       hasDualAudio(e, title),
	}

	// A release is a batch when it spans an episode range, or when it names a
	// season/anime but carries no single episode number (a season/complete pack).
	p.Batch = end > start || (start == 0 && looksLikePack(e, title))
	return p
}

// episodeRange converts anitogo's episode slice ("01-10" -> ["1","10"]) into a
// numeric [start,end]. A single number yields start == end; an empty slice 0,0.
func episodeRange(nums []string) (start, end int) {
	if len(nums) == 0 {
		return 0, 0
	}
	start = atoi(nums[0])
	end = atoi(nums[len(nums)-1])
	if end < start {
		end = start
	}
	return start, end
}

// looksLikePack reports whether a title with no single episode number still
// denotes a multi-episode release — a season pack or an explicit batch marker.
func looksLikePack(e *anitogo.Elements, title string) bool {
	for _, o := range e.Other {
		if strings.EqualFold(o, "batch") || strings.EqualFold(o, "complete") {
			return true
		}
	}
	lower := strings.ToLower(title)
	// A named season with no episode number reads as a full-season pack.
	return len(e.AnimeSeason) > 0 || strings.Contains(lower, "batch") || strings.Contains(lower, "complete")
}

func hasDualAudio(e *anitogo.Elements, title string) bool {
	for _, a := range e.AudioTerm {
		if strings.Contains(strings.ToLower(a), "dual") {
			return true
		}
	}
	return strings.Contains(strings.ToLower(title), "dual audio") ||
		strings.Contains(strings.ToLower(title), "dual-audio")
}

func firstInt(vals []string) int {
	if len(vals) == 0 {
		return 0
	}
	return atoi(vals[0])
}

func atoi(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	return n
}
