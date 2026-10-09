package shellport

import "crude/coreprotocol"

// ClientFilesChangedMsg invalidates a shell's file projections using core-owned
// watcher evidence. No client watcher or filesystem scan is implied.
type ClientFilesChangedMsg struct {
	Owner   Workspace
	Changes []coreprotocol.FileChange
}
