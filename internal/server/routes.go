package server

import (
	"log/slog"

	"github.com/danielgtaylor/huma/v2"

	"github.com/matthewdias/transpondarr/internal/core/auth"
	"github.com/matthewdias/transpondarr/internal/core/browse"
	"github.com/matthewdias/transpondarr/internal/core/catalog"
	"github.com/matthewdias/transpondarr/internal/core/clients"
	"github.com/matthewdias/transpondarr/internal/core/jobs"
	"github.com/matthewdias/transpondarr/internal/core/settings"
	"github.com/matthewdias/transpondarr/internal/store"
)

// routeDeps bundles what the handlers need. The clients registry supplies the
// live download/indexer clients (either may be nil when unconfigured; handlers
// report that as 503); settings backs the runtime-config endpoints; jobs backs
// the job-status endpoint and is nil on the spec-dump path.
type routeDeps struct {
	store    *store.Store
	log      *slog.Logger
	catalog  *catalog.Service
	browse   *browse.Service
	clients  *clients.Registry
	settings *settings.Service
	auth     *auth.Service
	jobs     *jobs.Runner
}

// registerRoutes wires every Huma endpoint. Handlers are grouped by resource in
// sibling *_routes.go files; this is just the manifest of which groups exist.
// Larger groups (series, settings) hang their handlers off a per-resource
// receiver struct; single-route groups keep them as inline closures.
func registerRoutes(api huma.API, deps routeDeps) {
	registerSystemRoutes(api, deps)
	registerMetadataRoutes(api, deps)
	registerBrowseRoutes(api, deps)
	registerIndexerRoutes(api, deps)
	registerDownloadRoutes(api, deps)
	registerSeriesRoutes(api, deps)
	registerSeriesAcquisitionRoutes(api, deps)
	registerCalendarRoutes(api, deps)
	registerSettingsRoutes(api, deps)
	registerProfileRoutes(api, deps)
}
