package coreprotocol

// FileChange is a workspace-owned invalidation, not a client filesystem event.
// Resync asks readers to refresh all loaded directories after watcher overflow.
type FileChange struct {
	Path    string `json:"path,omitempty"`
	Removed bool   `json:"removed,omitempty"`
	Git     bool   `json:"git,omitempty"`
	Resync  bool   `json:"resync,omitempty"`
}
