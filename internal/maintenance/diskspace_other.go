//go:build !linux

package maintenance

// freeBytes is unknown outside Linux (whose Statfs fields differ per platform); the container image always runs on Linux.
func freeBytes(string) (uint64, bool) { return 0, false }
