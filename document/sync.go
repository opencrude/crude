package document

// SyncResult carries every committed edit after a client's canonical revision,
// plus the latest savepoint. Reset is explicit when the edit history is missing.
type SyncResult struct {
	Snapshot Snapshot
	Changes  []Change
	Reset    bool
}
