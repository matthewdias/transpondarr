// Package web embeds the built frontend (web/dist) into the binary so
// Transpondarr ships as a single artifact. `make web` (Vite) populates dist;
// a committed web/dist/.gitkeep keeps the directory present so the `all:dist`
// embed below compiles on a fresh clone, and a copy in frontend/public/ makes
// every Vite build re-emit it (emptyOutDir wipes dist first).
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// DistFS returns the frontend build output rooted at dist/.
func DistFS() (fs.FS, error) {
	return fs.Sub(dist, "dist")
}
