package worktree

import "runtime"

// defaultDocumentMode is the permission a newly written regular file reports
// through os.Stat on this platform. Windows has no Unix permission bits and
// reports writable files as 0666; using 0644 there turned every new accepted
// document into a spurious mode change during review.
var defaultDocumentMode = func() uint32 {
	if runtime.GOOS == "windows" {
		return 0666
	}
	return 0644
}()
