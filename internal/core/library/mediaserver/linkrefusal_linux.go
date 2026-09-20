//go:build linux

package mediaserver

import "os"

// boundByFileOwner reports whether the kernel checks this process's ownership like
// anyone else's, which is what the capability set says.
func boundByFileOwner() bool {
	return boundByFileOwnerFrom(func() ([]byte, error) { return os.ReadFile("/proc/self/status") })
}
