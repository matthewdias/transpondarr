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
