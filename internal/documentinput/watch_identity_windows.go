//go:build windows

package documentinput

import (
	"fmt"
	"os"
	"syscall"
)

// watchFileIdentity names the file behind name by its NTFS/ReFS file ID
// (volume serial number and file index), so a replacement under the same name
// is recognized even when it carries the same timestamps. Opening a regular
// file cannot block here; on any error there is no identity.
func watchFileIdentity(root *os.Root, name string, _ os.FileInfo) string {
	file, err := root.Open(name)
	if err != nil {
		return ""
	}
	defer func() { _ = file.Close() }()
	var data syscall.ByHandleFileInformation
	if err = syscall.GetFileInformationByHandle(syscall.Handle(file.Fd()), &data); err != nil {
		return ""
	}
	return fmt.Sprintf("%d:%d:%d", data.VolumeSerialNumber, data.FileIndexHigh, data.FileIndexLow)
}
