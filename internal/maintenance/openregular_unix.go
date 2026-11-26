//go:build unix

package maintenance

import (
	"os"
	"syscall"
)

// nonblockingRead keeps the open of a named pipe without a writer from
// blocking.
const nonblockingRead = syscall.O_NONBLOCK

// clearNonblock returns a file confirmed as regular to blocking reads, so a
// network or FUSE mount cannot answer a read with EAGAIN.
func clearNonblock(f *os.File) error {
	conn, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var setErr error
	if err = conn.Control(func(fd uintptr) { setErr = syscall.SetNonblock(int(fd), false) }); err != nil {
		return err
	}
	return setErr
}
