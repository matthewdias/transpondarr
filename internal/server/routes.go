package server

import (
	"github.com/danielgtaylor/huma/v2"

	"github.com/matthewdias/transpondarr/internal/core/auth"
	"github.com/matthewdias/transpondarr/internal/core/catalog"
	"github.com/matthewdias/transpondarr/internal/core/clients"
	"github.com/matthewdias/transpondarr/internal/core/settings"
	"github.com/matthewdias/transpondarr/internal/store"
)

// routeDeps bundles what the handlers need. The clients registry supplies the
// live download/indexer clients (either may be nil when unconfigured; handlers
// report that as 503); settings backs the runtime-config endpoints.
type routeDeps struct {
	store    *store.Store
	catalog  *catalog.Service
	clients  *clients.Registry
	settings *settings.Service
	auth     *auth.Service
}

// registerRoutes wires every Huma endpoint. Handlers are grouped by resource in
// sibling *_routes.go files; this is just the manifest of which groups exist.
// Larger groups (series, settings) hang their handlers off a per-resource
// receiver struct; single-route groups keep them as inline closures.
func registerRoutes(api huma.API, deps routeDeps) {
	registerSystemRoutes(api)
	registerMetadataRoutes(api, deps)
	registerIndexerRoutes(api, deps)
	registerDownloadRoutes(api, deps)
	registerSeriesRoutes(api, deps)
	registerSeriesAcquisitionRoutes(api, deps)
	registerSettingsRoutes(api, deps)
}
