//go:build windows

package localhistory

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func fileIdentityKey(path string, _ os.FileInfo) (string, bool) {
	file, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer file.Close()
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &info); err != nil {
		return "", false
	}
	return fmt.Sprintf("%d:%d:%d", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow), true
}

// changeKey is unavailable from Windows FileInfo; size, modification time
// and file index identify unchanged content there.
func changeKey(os.FileInfo) int64 { return 0 }
