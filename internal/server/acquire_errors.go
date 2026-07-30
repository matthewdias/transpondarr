package server

import (
	"errors"

	"github.com/danielgtaylor/huma/v2"

	"github.com/matthewdias/transpondarr/internal/core/acquire"
)

// acquireHTTPError maps the acquire package's sentinels to status errors. It is
// the one place huma meets core: the service returns plain errors so the sweep
// can consume them too.
func acquireHTTPError(err error) error {
	switch {
	case errors.Is(err, acquire.ErrNoIndexer):
		return huma.Error503ServiceUnavailable(
			"no indexer configured (set TRANSPONDARR_TORZNAB_URL, _APIKEY)")
	case errors.Is(err, acquire.ErrNoDownloadClient):
		return huma.Error503ServiceUnavailable(
			"no download client configured (set it in Settings, or TRANSPONDARR_QBIT_URL/_USER/_PASSWORD)")
	case errors.Is(err, acquire.ErrSeriesNotFound):
		return huma.Error404NotFound("series not found")
	case errors.Is(err, acquire.ErrIndexerSearch):
		return huma.Error502BadGateway("indexer search failed", err)
	case errors.Is(err, acquire.ErrDownloadAdd):
		return huma.Error502BadGateway("download client add failed", err)
	default:
		return huma.Error500InternalServerError("acquisition failed", err)
	}
}
