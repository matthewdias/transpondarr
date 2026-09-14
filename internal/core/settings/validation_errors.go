package settings

import (
	"errors"
	"fmt"
)

// The settings checks a user fixes by editing a field. They are typed so the HTTP
// layer can word them without matching this text.
var (
	ErrDownloadURLRequired = errors.New("a qBittorrent URL is required")
	ErrIndexerURLRequired  = errors.New("a Torznab URL is required")
	ErrLibraryDirRequired  = errors.New("a library directory is required")
)

// CategoryError is a Newznab category list entry that isn't a positive integer.
type CategoryError struct {
	Value string
}

func (e *CategoryError) Error() string {
	return fmt.Sprintf("invalid category %q (want positive numeric Newznab ids, e.g. 5070)", e.Value)
}

// DirProblem is why TestLibrary can't use a library root.
type DirProblem int

// The ways a library root fails checkWritableDir.
const (
	DirInaccessible DirProblem = iota
	DirNotDirectory
	DirNotWritable
)

// DirError is a library root TestLibrary can't use.
type DirError struct {
	Root    string // "library" or "movies library"
	Path    string
	Problem DirProblem
	Err     error // the filesystem error; nil for DirNotDirectory
}

func (e *DirError) Error() string {
	switch e.Problem {
	case DirNotDirectory:
		return fmt.Sprintf("the %s path %q is not a directory", e.Root, e.Path)
	case DirNotWritable:
		return fmt.Sprintf("the %s directory %q is not writable: %v", e.Root, e.Path, e.Err)
	default:
		return fmt.Sprintf("cannot access the %s directory %q: %v", e.Root, e.Path, e.Err)
	}
}

func (e *DirError) Unwrap() error { return e.Err }
