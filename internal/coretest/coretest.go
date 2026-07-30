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

// --- fake download client ---------------------------------------------------

// FakeDownload records Add calls and returns canned results, so a test can drive
// the grab handoff without a real qBittorrent. Result/Err control Add; Statuses
// controls Status.
type FakeDownload struct {
	NameStr string
	Result  download.AddResult
	Err     error

	// Statuses is what Status returns (importer-facing). It is not filtered by
	// requested hash, which is enough for the pipeline tests.
	Statuses  []download.Status
	StatusErr error

	Adds []download.AddOptions // recorded, in call order
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
	f.Adds = append(f.Adds, opts)
	if f.Err != nil {
		return download.AddResult{}, f.Err
	}
	return f.Result, nil
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
	f.Placed = append(f.Placed, r)
	if f.DestErr != nil {
		return "", f.DestErr
	}
	return "/library/placed.mkv", nil
}
