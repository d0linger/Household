//go:build linux

package maintenance

import "syscall"

// freeBytes reports the space available to unprivileged writers at path.
func freeBytes(path string) (uint64, bool) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, false
	}
	return stat.Bavail * uint64(stat.Bsize), true // #nosec G115 -- block size is positive
}
