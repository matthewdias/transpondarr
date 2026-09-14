package server

import (
	"errors"

	"github.com/danielgtaylor/huma/v2"

	"github.com/matthewdias/transpondarr/internal/core/acquire"
)

// acquireHTTPError maps the acquire package's sentinels to status errors. It is
// the one boundary between huma and core: the service returns plain errors so
// the search sweep can use them too.
func acquireHTTPError(err error) error {
	switch {
	case errors.Is(err, acquire.ErrNoIndexer):
		return huma.Error503ServiceUnavailable(noIndexerDetail)
	case errors.Is(err, acquire.ErrNoDownloadClient):
		return huma.Error503ServiceUnavailable(noDownloadClientDetail)
	case errors.Is(err, acquire.ErrTitleNotFound):
		return huma.Error404NotFound(titleGoneDetail)
	case errors.Is(err, acquire.ErrIndexerSearch):
		return huma.Error502BadGateway("Couldn't search the indexer. Check Settings > Indexer.", err)
	case errors.Is(err, acquire.ErrDownloadAdd):
		return huma.Error502BadGateway("The download client didn't accept the release.", err)
	default:
		return storeError("finish the search or grab", err)
	}
}
