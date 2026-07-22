//go:build !linux

package privdrop

// Drop is Linux-only (it exists for the Docker image); elsewhere it never
// drops. See privdrop_linux.go.
func Drop(dataDir string) (uid, gid int, err error) {
	return -1, -1, nil
}
