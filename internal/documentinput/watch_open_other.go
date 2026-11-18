//go:build !unix

package documentinput

// watchOpenFlags adds nothing where opening a file cannot block on a FIFO.
const watchOpenFlags = 0
