//go:build windows

package documentinput

import (
	"fmt"
	"os"
	"syscall"
)

// watchFileIdentity uses the creation time on Windows: a replacement file is
// created anew, even when it later takes the old name, size and time.
func watchFileIdentity(info os.FileInfo) string {
	data, ok := info.Sys().(*syscall.Win32FileAttributeData)
	if !ok {
		return ""
	}
	return fmt.Sprintf("created:%d", data.CreationTime.Nanoseconds())
}
