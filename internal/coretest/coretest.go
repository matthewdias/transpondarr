// Package coretest provides shared test doubles and fixtures for exercising the
// acquisition pipeline (search → decide → grab → import) end to end. It supplies
// a real temp-file SQLite store plus in-memory fakes for the three pluggable
// clients — Indexer, download.Client, and library.Target — so a test can wire a
// whole pipeline without a network, a torrent client, or a media server.
//
// It is imported only from _test.go files; importing testing here is deliberate.
package coretest

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/matthewdias/transpondarr/internal/core/download"
	"github.com/matthewdias/transpondarr/internal/core/indexer"
	"github.com/matthewdias/transpondarr/internal/core/library"
	"github.com/matthewdias/transpondarr/internal/store"
)

// NewStore opens a fresh SQLite store in a temp dir (migrations applied) and
// registers cleanup. It is the shared replacement for the per-package
// newStore/tempStore helpers.
func NewStore(t testing.TB) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.DB.Close() })
	return st
}

// --- fake indexer -----------------------------------------------------------

// FakeIndexer returns a fixed set of releases (or an error) for every search and
// records the queries it was asked, standing in for a Torznab endpoint.
type FakeIndexer struct {
	NameStr  string
	Releases []indexer.Release
	Err      error

	// ByTerm, when non-nil, keys releases on the exact query term (unlisted terms
	// get zero results) so a test can exercise the variant-fallback search.
	ByTerm map[string][]indexer.Release
	// ErrByTerm fails only the listed terms, for errors mid-fallback.
	ErrByTerm map[string]error

	// SearchHook runs at the top of every Search. A test can drive another actor
	// from it to model work landing while a pass is out on the network.
	SearchHook func(indexer.Query)

	Queries []indexer.Query // recorded, in call order
}

var _ indexer.Indexer = (*FakeIndexer)(nil)

func (f *FakeIndexer) Name() string {
	if f.NameStr != "" {
		return f.NameStr
	}
	return "fake-indexer"
}

func (f *FakeIndexer) Search(_ context.Context, q indexer.Query) ([]indexer.Release, error) {
	if f.SearchHook != nil {
		f.SearchHook(q)
	}
	f.Queries = append(f.Queries, q)
	if f.Err != nil {
		return nil, f.Err
	}
	if err := f.ErrByTerm[q.Term]; err != nil {
		return nil, err
	}
	if f.ByTerm != nil {
		return f.ByTerm[q.Term], nil
	}
	return f.Releases, nil
}

// FakeFeed is a FakeIndexer that also publishes a recent feed. The plain
// FakeIndexer deliberately does not implement indexer.RecentFeed: that is what
// keeps the degrade-to-sweep-only path testable.
type FakeFeed struct {
	FakeIndexer
	Entries []indexer.FeedEntry
	FeedErr error

	Polls int // Recent call count, so a test can assert a quiet poll costs nothing
}

var _ indexer.RecentFeed = (*FakeFeed)(nil)

func (f *FakeFeed) Recent(context.Context) ([]indexer.FeedEntry, error) {
	f.Polls++
	if f.FeedErr != nil {
		return nil, f.FeedErr
	}
	return f.Entries, nil
}

// --- fake download client ---------------------------------------------------

// FakeDownload records Add calls and returns canned results, so a test can drive
// the grab handoff without a real qBittorrent. Result/Err control Add; Statuses
// controls Status.
type FakeDownload struct {
	NameStr string
	Result  download.AddResult
	Err     error

	// FailURLs fails only the listed download URLs, so a test can model one dead
	// release among healthy ones rather than a client that is down entirely.
	FailURLs map[string]error

	// Statuses is what Status returns (importer-facing). It is not filtered by
	// requested hash, which is enough for the pipeline tests.
	Statuses  []download.Status
	StatusErr error

	// AddHook runs at the top of every Add. A test can block in it to hold one
	// grab inside the client while another grab runs.
	AddHook func(download.AddOptions)

	// RemoveErr fails Remove, so a test can model a client that refuses deletes.
	RemoveErr error

	// mu guards Adds and Removes: with the claim registry under test, two grabs
	// can reach the client at once, and an unguarded slice would report that as a
	// data race rather than as the assertion failure it is.
	mu      sync.Mutex
	Adds    []download.AddOptions // recorded, in call order
	Removes []RemoveCall          // recorded, in call order
}

// RemoveCall records one Remove request handed to the fake client.
type RemoveCall struct {
	Hashes     []string
	DeleteData bool
}

var _ download.Client = (*FakeDownload)(nil)

func (f *FakeDownload) Name() string {
	if f.NameStr != "" {
		return f.NameStr
	}
	return "fake-download"
}

func (f *FakeDownload) Test(context.Context) error { return nil }

func (f *FakeDownload) Add(_ context.Context, opts download.AddOptions) (download.AddResult, error) {
	if f.AddHook != nil {
		f.AddHook(opts)
	}
	f.mu.Lock()
	f.Adds = append(f.Adds, opts)
	f.mu.Unlock()
	if err, ok := f.FailURLs[opts.URL]; ok {
		return download.AddResult{}, err
	}
	if f.Err != nil {
		return download.AddResult{}, f.Err
	}
	return f.Result, nil
}

// AddCount is the race-safe read of len(Adds), for tests that add concurrently.
func (f *FakeDownload) AddCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.Adds)
}

func (f *FakeDownload) Remove(_ context.Context, hashes []string, deleteData bool) error {
	f.mu.Lock()
	f.Removes = append(f.Removes, RemoveCall{Hashes: hashes, DeleteData: deleteData})
	f.mu.Unlock()
	return f.RemoveErr
}

func (f *FakeDownload) Status(_ context.Context, _ ...string) ([]download.Status, error) {
	if f.StatusErr != nil {
		return nil, f.StatusErr
	}
	return f.Statuses, nil
}

// --- fake library target ----------------------------------------------------

// FakeLibrary records the import requests it was handed and returns a fixed
// destination path, standing in for the media-server layout target.
type FakeLibrary struct {
	NameStr string
	DestErr error

	// PlaceHook runs at the top of every Place. A test can drive another actor
	// from it to model work landing while an import is at its point of no return.
	PlaceHook func(library.ImportRequest)

	Placed []library.ImportRequest // recorded, in call order
}

var _ library.Target = (*FakeLibrary)(nil)

func (f *FakeLibrary) Name() string {
	if f.NameStr != "" {
		return f.NameStr
	}
	return "fake-library"
}

func (f *FakeLibrary) Place(_ context.Context, r library.ImportRequest) (string, error) {
	if f.PlaceHook != nil {
		f.PlaceHook(r)
	}
	f.Placed = append(f.Placed, r)
	if f.DestErr != nil {
		return "", f.DestErr
	}
	return "/library/placed.mkv", nil
}
