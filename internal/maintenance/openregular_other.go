//go:build !unix

package maintenance

import "os"

// nonblockingRead is not needed where opening a file cannot wait for a writer.
const nonblockingRead = 0

func clearNonblock(*os.File) error { return nil }
