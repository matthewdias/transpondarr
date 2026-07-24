// Package domain holds Transpondarr's content-type-agnostic core model.
//
// The pipeline (search -> decide -> grab -> import) is keyed on WantedItem, never
// on a hardcoded "episode". An episode is one WantedItem; a movie (a later
// format) is a Title with a single WantedItem — so adding movies is a new
// Format plus a naming template, not a re-architecture.
package domain

// Format is the shape of a title. MOVIE is reserved for a later phase; the model
// already accommodates it.
type Format string

const (
	FormatTV      Format = "TV"
	FormatOVA     Format = "OVA"
	FormatONA     Format = "ONA"
	FormatSpecial Format = "SPECIAL"
	FormatMovie   Format = "MOVIE" // reserved: not handled in v1
)

// WantedKind distinguishes the kind of acquirable item.
type WantedKind string

const (
	KindEpisode WantedKind = "episode"
	KindMovie   WantedKind = "movie"
)

// Title is a tracked work (from AniList). It owns a set of WantedItems.
type Title struct {
	ID        int64
	AniListID int64
	Name      string
	Format    Format
	Monitored bool
	Items     []WantedItem
}

// WantedItem is a single acquirable unit — an episode, or a movie's single file.
type WantedItem struct {
	ID     int64
	Kind   WantedKind
	Number int // absolute/episode number; typically 1 for a movie
	Name   string
	Have   bool
}

// QualityProfile is what a user wants a release to be. Release group is the
// dominant axis for anime — a trusted group's 720p beats an unknown group's
// 1080p — so Groups is ordered and everything else is secondary.
type QualityProfile struct {
	ID              int64
	Name            string
	Groups          []string // preferred release groups, most preferred first
	BlockedGroups   []string // never take, at any quality
	ResolutionOrder []string // best first; an unlisted resolution scores zero
	PreferredSource string   // "web", "bd", "tv" or "dvd"; "" for no preference
	SubPref         string   // "softsub" or "hardsub"; "" for no preference
	PreferDualAudio bool
	CodecPref       string   // "h264", "h265" or "av1"; "" for no preference
	HardExcludes    []string // axis tokens (e.g. "hardsub") a release must never carry
	MinScore        int      // floor: a candidate scoring below this is ineligible
}
