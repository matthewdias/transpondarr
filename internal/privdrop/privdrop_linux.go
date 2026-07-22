//go:build linux

// Package privdrop lets the container start as root just long enough to fix
// ownership of the data directory, then sheds privileges — the in-binary
// equivalent of the PUID/PGID init step in linuxserver-style images, needed
// because the distroless image has no shell or init to do it externally.
package privdrop

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

// Drop is a no-op unless TRANSPONDARR_PRIVDROP=1 (set by the Dockerfile — a
// bare-metal root run keeps today's behavior) and the process is actually root
// (someone passing --user opted out of the root phase). Otherwise it chowns
// dataDir recursively to PUID:PGID (env, default 1000:1000) and drops the
// process to that uid/gid. Setting PUID=0 opts out and keeps the process as
// root.
//
// The returned uid is -1 when no drop happened.
func Drop(dataDir string) (uid, gid int, err error) {
	if os.Getenv("TRANSPONDARR_PRIVDROP") != "1" || os.Geteuid() != 0 {
		return -1, -1, nil
	}
	if uid, err = envID("PUID", 1000); err != nil {
		return -1, -1, err
	}
	if gid, err = envID("PGID", 1000); err != nil {
		return -1, -1, err
	}
	if uid == 0 {
		return -1, -1, nil
	}

	if err := chownTree(dataDir, uid, gid); err != nil {
		return -1, -1, fmt.Errorf("chown data dir: %w", err)
	}

	// Order matters: gids first — after Setuid the process may no longer have
	// permission to change them. Since Go 1.16 these apply to all threads.
	if err := syscall.Setgroups([]int{gid}); err != nil {
		return -1, -1, fmt.Errorf("setgroups %d: %w", gid, err)
	}
	if err := syscall.Setgid(gid); err != nil {
		return -1, -1, fmt.Errorf("setgid %d: %w", gid, err)
	}
	if err := syscall.Setuid(uid); err != nil {
		return -1, -1, fmt.Errorf("setuid %d: %w", uid, err)
	}
	return uid, gid, nil
}

func envID(key string, def int) (int, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	id, err := strconv.Atoi(v)
	if err != nil || id < 0 {
		return 0, fmt.Errorf("%s must be a non-negative integer, got %q", key, v)
	}
	return id, nil
}

// chownTree chowns dir and everything under it. Lchown so a symlink inside the
// data dir changes ownership itself rather than of whatever it points at.
func chownTree(dir string, uid, gid int) error {
	return filepath.WalkDir(dir, func(path string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Lchown(path, uid, gid)
	})
}
