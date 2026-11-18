//go:build !unix

package documentinput

import "os"

// watchFileIdentity has no portable identity here; size and time decide.
func watchFileIdentity(os.FileInfo) string { return "" }
