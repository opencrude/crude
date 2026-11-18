package shellport

// Presence is the shell's side of the desktop-notification election that the
// core runs for each workspace. See docs/reference/desktop-notifications.md.
type Presence interface {
	// ReportPresence tells the core whether this shell's user is present and
	// whether this shell can show desktop notifications. It returns at once;
	// the latest report reaches the core in order and again after reconnect.
	ReportPresence(present, notifies bool)
}

// PresenceChangedMsg publishes the core's election to the shell. Present
// means that the user is present in an attached shell. Notify means that
// this shell, and no other, shows desktop notifications for the workspace.
type PresenceChangedMsg struct {
	Owner   Workspace
	Present bool
	Notify  bool
}
