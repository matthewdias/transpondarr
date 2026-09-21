//go:build linux

package mediaserver

import "os"

// boundByFileOwner reports whether the kernel checks this process's ownership like
// anyone else's, reading that from the process's own capability set.
func boundByFileOwner() bool {
	return boundByFileOwnerFrom(func() ([]byte, error) { return os.ReadFile("/proc/self/status") })
}
