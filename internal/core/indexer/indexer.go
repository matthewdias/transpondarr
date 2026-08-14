// Package indexer defines the pluggable indexer interface. The only concrete
// adapter today is a generic Torznab adapter (breadth via Prowlarr/Jackett)
package indexer

import (
	"context"
	"time"
)

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

// FeedEntry is one item from an indexer's recent feed: a release plus the feed
// metadata that makes "new since the last poll" answerable. It stays off Release
// because a GUID and a publish time mean nothing on the search path, which is
// where Release is otherwise used end to end.
type FeedEntry struct {
	Release   Release
	GUID      string    // the feed's own identifier; empty when it published none
	Published time.Time // zero when the feed omitted a pubDate
}

// RecentFeed is an optional Indexer capability: sources that can list their
// newest releases without a search term implement it. It stays off Indexer
// because not every source has a feed, and because one request answering for the
// whole library is a different cost model from one search per title.
//
// Two rules follow, as for metadata.AiringProvider. A caller treats a missing
// capability as a supported configuration, not an error — degrade to the
// scheduled sweep. And any future decorator around an Indexer must forward this
// conditionally, so the type assertion never claims a feed the adapter
// underneath cannot serve.
type RecentFeed interface {
	// Recent returns the newest releases the source is publishing, newest first
	// where the source orders them.
	Recent(ctx context.Context) ([]FeedEntry, error)
}
