// Package download defines the pluggable download-client interface.
//
// Torrents are keyed by their info hash — the one identifier that is stable
// across clients and survives rename/move — so the pipeline never has to reason
// about client-specific IDs. Add derives that hash locally.
package download

import (
	"context"
	"errors"
)

// ErrBadRelease marks an Add failure the release caused, not the adapter's own
// connectivity or a client rejecting the add — a caller remembers these, so
// mismarking would blocklist healthy releases (#120).
var ErrBadRelease = errors.New("download: release could not be resolved")

type AddOutcome string

const (
	// AddSuccess means the torrent was accepted by the client.
	AddSuccess AddOutcome = "success"
	// AddAlreadyExists means the client was already managing this info hash.
	AddAlreadyExists AddOutcome = "already_exists"
)

// AddOptions describes a torrent to add. Exactly one of URL or Content is used;
// Content wins when both are set. A magnet URL carries its own info hash; for a
// .torrent (URL or Content) the adapter derives the hash from the metainfo.
type AddOptions struct {
	// URL is a magnet link or an http(s) URL to a .torrent file.
	URL string
	// Content is raw .torrent bytes, used when the caller already has the file.
	Content []byte
	// Category tags the torrent so Transpondarr-managed downloads are
	// identifiable in the client (qBit category, Deluge label, etc.).
	Category string
	// SavePath overrides the client's default download directory. It must be
	// valid in the *client's* filesystem context — that shared path context is
	// what makes the seeding-safe hardlink import work.
	SavePath string
	// Paused adds the torrent without starting it.
	Paused bool
}

// AddResult reports what happened when adding a torrent.
type AddResult struct {
	Hash    string
	Outcome AddOutcome
}

// State is a normalized, client-agnostic download state so the pipeline and the
// UI never branch on a specific client's status vocabulary.
type State string

const (
	StateDownloading State = "downloading"
	StateComplete    State = "complete" // finished; may be seeding
	StateStalled     State = "stalled"
	StateChecking    State = "checking"
	StatePaused      State = "paused"
	StateError       State = "error"
	StateUnknown     State = "unknown"
)

// Status is a point-in-time, client-agnostic view of a download.
type Status struct {
	Hash     string
	Name     string
	State    State
	Progress float64 // 0..1
	SavePath string
	// ContentPath is the on-disk path to the downloaded content (file or root
	// folder) in the client's filesystem context — the import pipeline
	// hardlinks from here.
	ContentPath string
}

// Client is a download client Transpondarr can drive.
type Client interface {
	// Name identifies the client implementation, e.g. "qbittorrent".
	Name() string
	// Test verifies connectivity and credentials.
	Test(ctx context.Context) error
	// Add injects a torrent and returns its info hash and the outcome.
	Add(ctx context.Context, opts AddOptions) (AddResult, error)
	// Status returns the current state of the requested hashes. Hashes the
	// client does not know are omitted from the result. With no hashes, it
	// returns every torrent the client is managing.
	Status(ctx context.Context, hashes ...string) ([]Status, error)
	// Remove deletes the given torrents, and their payload data when deleteData
	// is set. Hashes the client does not know are ignored.
	Remove(ctx context.Context, hashes []string, deleteData bool) error
}
