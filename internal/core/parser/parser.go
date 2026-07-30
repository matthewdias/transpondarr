// Package parser turns a raw release title into structured fields — title,
// season, episode number(s), release group, and the quality axes (resolution,
// source, subtitle type, codec, version/repack, dual-audio) — using
// anitogo (a Go port of Anitomy): anime filename conventions are a large
// heuristic problem better handled by a maintained library than by hand.
//
// The parser is deliberately dumb about *identity*: it extracts what the release
// name says, and never decides which series or wanted item it belongs to — that
// reconciliation is the decide layer's job.
package parser

import (
	"regexp"
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

	// ResolutionRaw is the release's literal form when Resolution was folded
	// from it ("1920x1036", "BD1080p", "4K"); "" when it was already canonical.
	ResolutionRaw string

	Season       int // 0 when unspecified
	EpisodeStart int // first episode number; 0 when none
	EpisodeEnd   int // last episode number; == EpisodeStart for a single episode

	// AbsoluteEpisode is anitogo's alternate number — the absolute count when the
	// title gives a season-relative number plus an absolute one (e.g. "S3 - 01 (51)").
	// 0 when absent.
	AbsoluteEpisode int

	Batch     bool // a multi-episode release (range, or a season pack with no number)
	DualAudio bool

	Source   string // "web", "bd", "tv" or "dvd"; "" when unstated
	Subs     string // "hardsub" or "softsub"; "" when unstated
	MultiSub bool   // multiple subtitle tracks advertised
	Codec    string // "h264", "h265" or "av1"; "" when unstated or other
	Version  int    // release version (2 for a v2); 0 when unmarked
	Repack   bool   // REPACK / PROPER re-release
}

// Parse extracts structured fields from a release title.
func Parse(title string) Parsed {
	e := anitogo.Parse(title, anitogo.DefaultOptions)

	start, end := episodeRange(e.EpisodeNumber)
	res, resRaw := normalizeResolution(e.VideoResolution)
	// anitogo classes 4K as a video term, so it names the tier only when no
	// digit form did.
	if res == "" {
		for _, term := range e.VideoTerm {
			if strings.EqualFold(term, "4K") {
				res, resRaw = "2160p", term
				break
			}
		}
	}
	p := Parsed{
		Title:           strings.TrimSpace(e.AnimeTitle),
		Group:           e.ReleaseGroup,
		Resolution:      res,
		ResolutionRaw:   resRaw,
		Season:          firstInt(e.AnimeSeason),
		EpisodeStart:    start,
		EpisodeEnd:      end,
		AbsoluteEpisode: firstInt(e.EpisodeNumberAlt),
		DualAudio:       hasDualAudio(e, title),
		Version:         firstInt(e.ReleaseVersion),
	}

	// Post-passes scan the title with the parsed series and episode names
	// removed, so e.g. "Ghost Web" cannot satisfy a source/codec token.
	rem := remainderOf(title, e)
	p.Source = sourceFrom(e, rem)
	p.Subs, p.MultiSub = subsFrom(e, rem)
	p.Codec = codecFrom(e, rem)
	p.Repack = repackRe.MatchString(rem)

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

// Standard tiers by height, plus the widths that still name a tier when the
// height is cropped (a 1920x1036 encode is a 1080p release).
var (
	resHeights = map[int]bool{360: true, 480: true, 576: true, 720: true, 1080: true, 1440: true, 2160: true}
	resWidths  = map[int]string{640: "480p", 704: "480p", 720: "480p", 848: "480p", 1280: "720p", 1920: "1080p", 3840: "2160p"}
)

// heightSuffix pulls the height out of anitogo's start-unanchored matches, so a
// glued prefix ("BD1080p") folds instead of escaping as junk.
var heightSuffix = regexp.MustCompile(`(\d{3,4})[pP]$`)

// normalizeResolution folds anitogo's dimension form to the height form quality
// profiles are written in; raw is the original, reported only when it was folded.
func normalizeResolution(s string) (res, raw string) {
	s = strings.TrimSpace(s)
	if w, h, ok := dimensions(s); ok {
		if !resHeights[h] {
			if tier, ok := resWidths[w]; ok {
				return tier, s
			}
		}
		return strconv.Itoa(h) + "p", s
	}
	// No height either ("a000x000", fuzzer-found): no profile axis could ever
	// match, and reporting nothing beats reporting junk.
	m := heightSuffix.FindStringSubmatch(s)
	if m == nil {
		return "", ""
	}
	res = m[1] + "p"
	if strings.EqualFold(s, res) {
		return res, ""
	}
	return res, s
}

// dimensions splits a "1920x1080" token; ok is false for anything else, which is
// how the already-canonical "1080p" form passes through untouched.
func dimensions(s string) (w, h int, ok bool) {
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == 'x' || r == 'X' || r == '×' })
	if len(parts) != 2 {
		return 0, 0, false
	}
	var err error
	if w, err = strconv.Atoi(parts[0]); err != nil {
		return 0, 0, false
	}
	if h, err = strconv.Atoi(parts[1]); err != nil {
		return 0, 0, false
	}
	return w, h, w > 0 && h > 0
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

// anitogo's keyword tables miss the bare WEB / WEB-DL tag, REPACK/PROPER, and
// AV1 entirely, and tokenize scene-style dot names unevenly — hence the raw-title
// regex fallbacks below.
var (
	webRe      = regexp.MustCompile(`\bweb(?:[-_. ]?dl)?\b`)
	webDLRe    = regexp.MustCompile(`\bweb[-_. ]?dl\b`)
	repackRe   = regexp.MustCompile(`\b(?:repack|proper)\b`)
	av1Re      = regexp.MustCompile(`\bav1\b`)
	h264Re     = regexp.MustCompile(`\b(?:[xh][ .]?264|avc)\b`)
	h265Re     = regexp.MustCompile(`\b(?:[xh][ .]?265|hevc)\b`)
	hardsubRe  = regexp.MustCompile(`\bhard[-_. ]?subs?\b`)
	softsubRe  = regexp.MustCompile(`\bsoft[-_. ]?subs?\b`)
	multiSubRe = regexp.MustCompile(`\bmulti[-_. ]?subs?\b`)
)

// remainderOf lowercases a raw title and removes the parsed series and episode
// names, so token scans cannot match words that belong to the show itself.
// Scene delimiters are folded to spaces first: anitogo joins parsed names with
// spaces, so "Ghost Web" would never match a dot-named "Ghost.Web" verbatim.
func remainderOf(raw string, e *anitogo.Elements) string {
	rem := foldDelims(raw)
	strip := []string{strings.TrimSpace(foldDelims(e.AnimeTitle))}
	// anitogo misfiles unrecognized scene tags (REPACK, x264-GRP) as episode
	// titles; only a multi-word episode title free of tag tokens is trusted as
	// a real name — a dual-title release can misfile a whole multi-word tag run.
	if t := strings.TrimSpace(foldDelims(e.EpisodeTitle)); strings.Contains(t, " ") && !tagRun(t) {
		strip = append(strip, t)
	}
	for _, t := range strip {
		if t != "" {
			rem = strings.Replace(rem, t, " ", 1)
		}
	}
	return rem
}

func foldDelims(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '.' || r == '_' {
			return ' '
		}
		return r
	}, strings.ToLower(s))
}

// tagRun reports whether a folded episode title reads as a misfiled scene tag
// run. Only tokens no real episode name carries qualify — strict WEB-DL, a
// codec, a sub-type — never bare "web" ("The Web of Fate" is a real name).
func tagRun(t string) bool {
	return webDLRe.MatchString(t) || h264Re.MatchString(t) || h265Re.MatchString(t) ||
		av1Re.MatchString(t) || hardsubRe.MatchString(t) || softsubRe.MatchString(t) ||
		multiSubRe.MatchString(t)
}

func sourceFrom(e *anitogo.Elements, rem string) string {
	for _, s := range e.Source {
		switch v := strings.ToLower(s); {
		case v == "bd" || v == "bdrip" || strings.HasPrefix(v, "blu"):
			return "bd"
		case strings.Contains(v, "dvd") || strings.HasPrefix(v, "r2j"):
			return "dvd"
		case strings.Contains(v, "tv"):
			return "tv"
		case strings.HasPrefix(v, "web"):
			return "web"
		}
	}
	if webRe.MatchString(rem) {
		return "web"
	}
	return ""
}

func subsFrom(e *anitogo.Elements, rem string) (subs string, multi bool) {
	for _, s := range e.Subtitles {
		switch v := strings.ToLower(s); {
		case strings.HasPrefix(v, "hardsub"):
			subs = "hardsub"
		case strings.HasPrefix(v, "softsub"):
			subs = "softsub"
		case strings.HasPrefix(v, "multi"):
			multi = true
		}
	}
	if subs == "" && hardsubRe.MatchString(rem) {
		subs = "hardsub"
	}
	if subs == "" && softsubRe.MatchString(rem) {
		subs = "softsub"
	}
	return subs, multi || multiSubRe.MatchString(rem)
}

func codecFrom(e *anitogo.Elements, rem string) string {
	for _, v := range e.VideoTerm {
		switch {
		case h265Re.MatchString(strings.ToLower(v)):
			return "h265"
		case h264Re.MatchString(strings.ToLower(v)):
			return "h264"
		}
	}
	switch {
	case h265Re.MatchString(rem):
		return "h265"
	case h264Re.MatchString(rem):
		return "h264"
	case av1Re.MatchString(rem):
		return "av1"
	}
	return ""
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

// atoi treats unparseable or negative values as "absent" (0) — anitogo can emit
// a negative token (e.g. "-1" from "(-1)0") that must not reach the decide layer.
func atoi(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 0 {
		return 0
	}
	return n
}
