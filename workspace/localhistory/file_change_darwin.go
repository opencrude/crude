package localhistory

import (
	"os"
	"syscall"
)

// The inode change time advances even if a writer restores the original mtime.
func changeKey(info os.FileInfo) int64 {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0
	}
	return stat.Ctimespec.Nano()
}
