package server

import "github.com/matthewdias/transpondarr/internal/store/db"

// itemState is one wanted item's derived acquisition state — the shared
// vocabulary (have / downloading / stuck / deferred / wanted) rendered by both
// series detail and the calendar. Derive it here only, so the edges never drift.
type itemState struct {
	Status       string
	ReleaseTitle string
	ImportError  string
}

// deriveItemState maps an item's have flag and its grab (hasGrab=false when
// none) to the status vocabulary.
func deriveItemState(have bool, grab db.Grab, hasGrab bool) itemState {
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
	case have && grabStatus == "grabbed":
		// An upgrade in flight over a file we hold (#97). Every other held state
		// stays "have": a deferred or failed upgrade left the library as it was.
		status = "downloading"
	case have:
		status = "have"
	case grabStatus == "import_deferred":
		// Settled without an import (a batch payload): distinct from
		// downloading, which would otherwise show as in-progress forever.
		status = "deferred"
	case importError != "":
		// Download done but the import keeps failing (path mapping, library
		// permissions): distinct from downloading, with the reason attached.
		status = "stuck"
	case releaseTitle != "":
		// A grab exists but the item isn't had yet → still downloading/importing.
		status = "downloading"
	}
	if status != "stuck" {
		// The reason is part of the stuck contract; a settled item must not
		// carry a stale one.
		importError = ""
	}
	return itemState{Status: status, ReleaseTitle: releaseTitle, ImportError: importError}
}
