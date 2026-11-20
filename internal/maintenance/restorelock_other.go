//go:build !unix

package maintenance

// acquireRestoreLock is a no-op outside Unix; the backup service image runs on
// Linux, where the lock is a real exclusive file lock on the work volume.
func acquireRestoreLock() (func(), error) { return func() {}, nil }
