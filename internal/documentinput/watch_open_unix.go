//go:build unix

package documentinput

import "syscall"

// watchOpenFlags opens inbox candidates without blocking: a FIFO swapped in
// after the regular-file check must not hold the shared worker in open(2).
const watchOpenFlags = syscall.O_NONBLOCK
