// Package clients holds the live download/indexer/library clients behind a
// mutex so the settings layer can rebuild and swap them at runtime — a config
// change takes effect without restarting the process. Consumers (HTTP handlers,
// the importer) read the current client on each use rather than capturing one,
// so a swap is picked up on the next request or poll.
//
// Any of the four may be nil when the corresponding integration is not
// configured; callers must nil-check.
package clients

import (
	"sync"

	"github.com/matthewdias/transpondarr/internal/core/download"
	"github.com/matthewdias/transpondarr/internal/core/indexer"
	"github.com/matthewdias/transpondarr/internal/core/library"
	"github.com/matthewdias/transpondarr/internal/core/notify"
)

// Registry is the swappable holder of the four pluggable clients.
type Registry struct {
	mu  sync.RWMutex
	dl  download.Client
	idx indexer.Indexer
	lib library.Target
	ntf *notify.Dispatcher
}

// New returns an empty registry (all clients nil until set).
func New() *Registry { return &Registry{} }

// Download returns the current download client, or nil when unconfigured.
func (r *Registry) Download() download.Client {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.dl
}

// Indexer returns the current indexer, or nil when unconfigured.
func (r *Registry) Indexer() indexer.Indexer {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.idx
}

// Library returns the current library target, or nil when unconfigured.
func (r *Registry) Library() library.Target {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.lib
}

// Notify returns the current notification dispatcher, or nil when no adapter is
// configured. Concrete rather than an interface, so nil is just nil (see
// buildLibrary for the typed-nil gotcha this sidesteps).
func (r *Registry) Notify() *notify.Dispatcher {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.ntf
}

// SetDownload swaps the download client (pass nil to disable).
func (r *Registry) SetDownload(c download.Client) {
	r.mu.Lock()
	r.dl = c
	r.mu.Unlock()
}

// SetIndexer swaps the indexer (pass nil to disable).
func (r *Registry) SetIndexer(c indexer.Indexer) {
	r.mu.Lock()
	r.idx = c
	r.mu.Unlock()
}

// SetLibrary swaps the library target (pass nil to disable).
func (r *Registry) SetLibrary(c library.Target) {
	r.mu.Lock()
	r.lib = c
	r.mu.Unlock()
}

// SetNotify swaps the notification dispatcher (pass nil to disable).
func (r *Registry) SetNotify(d *notify.Dispatcher) {
	r.mu.Lock()
	r.ntf = d
	r.mu.Unlock()
}
