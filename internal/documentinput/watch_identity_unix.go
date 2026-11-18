//go:build unix

package documentinput

import (
	"fmt"
	"os"
	"syscall"
)

// watchFileIdentity names the file behind info (device and inode; the field
// types differ across Unix targets, hence the conversions), so a
// replacement under the same name is recognized.
func watchFileIdentity(info os.FileInfo) string {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	return fmt.Sprintf("%d:%d", uint64(stat.Dev), uint64(stat.Ino))
}
