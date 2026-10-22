//go:build !linux && !windows

package localhistory

import "os"

// changeKey is not used where the change time is not portable; size,
// modification time and file identity decide.
func changeKey(os.FileInfo) int64 { return 0 }
