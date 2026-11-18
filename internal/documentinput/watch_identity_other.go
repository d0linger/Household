//go:build !unix && !windows

package documentinput

import "os"

// watchFileIdentity has no file identity on these platforms, where the watch
// inbox is unsupported (Household ships Linux images; Windows and Unix track
// identity).
func watchFileIdentity(*os.Root, string, os.FileInfo) string { return "" }
