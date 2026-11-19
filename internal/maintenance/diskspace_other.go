//go:build !unix

package maintenance

// freeBytes is unknown outside Unix; the container image always runs on Linux.
func freeBytes(string) (uint64, bool) { return 0, false }
