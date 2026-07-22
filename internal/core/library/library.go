// Package library defines the final stage of the import pipeline: the
// LibraryTarget interface. The universal pipeline (parse -> map -> hardlink) hands a
// placed file to a Target.
package library

import (
	"context"

	"github.com/matthewdias/transpondarr/internal/core/domain"
)

// ImportRequest is a file ready to be placed into the library.
type ImportRequest struct {
	SourcePath string
	Title      domain.Title
	Item       domain.WantedItem
}

// Target places/organizes an imported file for a downstream media server.
type Target interface {
	Name() string
	Place(ctx context.Context, req ImportRequest) (finalPath string, err error)
}
