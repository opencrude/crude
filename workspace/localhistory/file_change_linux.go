package localhistory

import (
	"os"
	"syscall"
)

// changeKey is the inode change time: content rewrites that preserve the
// modification time still update it.
func changeKey(info os.FileInfo) int64 {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0
	}
	return stat.Ctim.Sec*1e9 + stat.Ctim.Nsec
}
