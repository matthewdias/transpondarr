// Package version exposes the build version, overridable via -ldflags at build
// time (see .goreleaser.yaml and the Makefile).
package version

// Version is the build version stamped in via -ldflags from the release tag;
// "dev" means an un-stamped build (e.g. the air dev server).
var Version = "dev"
