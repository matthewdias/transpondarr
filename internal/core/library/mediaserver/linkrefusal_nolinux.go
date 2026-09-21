//go:build unix && !linux

package mediaserver

// boundByFileOwner is false off Linux, whose fs.protected_hardlinks is the only
// thing the diagnosis names. macOS and the BSDs refuse a link for other reasons.
func boundByFileOwner() bool { return false }
