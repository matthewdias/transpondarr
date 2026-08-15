// Package library defines the final stage of the import pipeline: the
// LibraryTarget interface. The universal pipeline (parse -> map -> hardlink) hands a
// placed file to a Target.
package library

import (
	"context"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/domain"
)

// ImportRequest is a file ready to be placed into the library.
type ImportRequest struct {
	SourcePath string
	Title      domain.Title
	Item       domain.WantedItem
	// Replace means this file supersedes one the library already holds (#97), so
	// a Target must overwrite rather than treat the destination as done.
	Replace bool
}

// Target places/organizes an imported file for a downstream media server.
type Target interface {
	Name() string
	Place(ctx context.Context, req ImportRequest) (finalPath string, err error)
}

// StagingSweeper is an optional Target capability: dropping the staging files an
// interrupted transfer left behind (#132). A type assertion rather than a wider
// Target, which stays write-only — enumerating a library is #170's question, not
// this one's — so a Target without it is a supported configuration, not an error.
type StagingSweeper interface {
	SweepStaging(ctx context.Context, olderThan time.Duration) (removed int, err error)
}
