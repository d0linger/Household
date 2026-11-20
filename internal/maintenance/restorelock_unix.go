//go:build unix

package maintenance

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// acquireRestoreLock takes the exclusive live-restore lock on the work volume.
// The backup loop and an operator's `household restore` (possibly another
// container on the same volume) can never run or recover a restore at once.
// It returns errRestoreBusy when another process holds the lock.
func acquireRestoreLock() (func(), error) {
	// A custom HOUSEHOLD_BACKUP_WORK may not exist yet before the first backup.
	if err := os.MkdirAll(workDirectory(), 0700); err != nil { // #nosec G301 -- private operator work directory
		return nil, err
	}
	path := filepath.Join(workDirectory(), "restore.lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600) // #nosec G304 -- fixed name in the operator work directory
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil { // #nosec G115 -- file descriptor fits int
		_ = file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errRestoreBusy
		}
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN) // #nosec G115 -- file descriptor fits int
		_ = file.Close()
	}, nil
}
