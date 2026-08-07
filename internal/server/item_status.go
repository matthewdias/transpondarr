package server

import "github.com/matthewdias/transpondarr/internal/store/db"

// itemState is one wanted item's derived acquisition state — the shared
// vocabulary (in_library / downloading / stuck / deferred / wanted) rendered by
// both series detail and the calendar. Derive it here only, so the edges never
// drift.
type itemState struct {
	Status       string
	ReleaseTitle string
	ImportError  string
}

// deriveItemState maps an item's in_library flag and its grab (hasGrab=false
// when none) to the status vocabulary.
func deriveItemState(inLibrary bool, grab db.Grab, hasGrab bool) itemState {
	var releaseTitle, grabStatus, importError string
	// A failed grab does not count as downloading: the item reverts to
	// "wanted" so it can be searched/grabbed again (the failure stays in the
	// grabs history). Only a non-failed grab marks the item downloading.
	if hasGrab && grab.Status != "failed" {
		releaseTitle = grab.ReleaseTitle
		grabStatus = grab.Status
		importError = grab.LastError.String
	}
	status := "wanted"
	switch {
	case inLibrary && grabStatus == "grabbed":
		// An upgrade in flight over a file we hold (#97). Every other held state
		// stays "in_library": a deferred or failed upgrade left the library alone.
		status = "downloading"
	case inLibrary:
		status = "in_library"
	case grabStatus == "import_deferred":
		// Settled without an import (a batch payload): distinct from
		// downloading, which would otherwise show as in-progress forever.
		status = "deferred"
	case importError != "":
		// Download done but the import keeps failing (path mapping, library
		// permissions): distinct from downloading, with the reason attached.
		status = "stuck"
	case releaseTitle != "":
		// A grab exists but the file isn't in the library yet → still downloading.
		status = "downloading"
	}
	if status != "stuck" {
		// The reason is part of the stuck contract; a settled item must not
		// carry a stale one.
		importError = ""
	}
	return itemState{Status: status, ReleaseTitle: releaseTitle, ImportError: importError}
}
