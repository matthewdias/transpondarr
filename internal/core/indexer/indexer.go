// Package indexer defines the pluggable indexer interface. The only concrete
// adapter today is a generic Torznab adapter (breadth via Prowlarr/Jackett)
package indexer

import "context"

// Query describes a search. It will grow season/episode/absolute fields as the
// mapping engine lands.
type Query struct {
	Term string
}

// Release is a single indexer result. Anime-specific attributes are populated by
// the release-parsing layer (Anitomy) downstream.
type Release struct {
	Title       string
	DownloadURL string
	InfoHash    string
	Size        int64
	Seeders     int
	Indexer     string

	// Parsed attributes (filled by the parser layer):
	ReleaseGroup string
	Resolution   string
	DualAudio    bool
}

// Indexer is any release source Transpondarr can search.
type Indexer interface {
	Name() string
	Search(ctx context.Context, q Query) ([]Release, error)
}
