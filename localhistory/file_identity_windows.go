//go:build windows

package localhistory

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// fileIdentityKey opens the file for attributes only, as os.SameFile does:
// a read open makes the antivirus filter scan each file and fails while
// another program holds the file without read sharing.
func fileIdentityKey(path string, _ os.FileInfo) (string, bool) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", false
	}
	handle, err := windows.CreateFile(name, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", false
	}
	defer func() { _ = windows.CloseHandle(handle) }()
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return "", false
	}
	return fmt.Sprintf("%d:%d:%d", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow), true
}

// changeKey is unavailable from Windows FileInfo; size, modification time
// and file index identify unchanged content there.
func changeKey(os.FileInfo) int64 { return 0 }
