package coreprotocol

// Shell presence decides which shell shows desktop notifications. Every
// interactive shell reports whether its user is present; the core publishes
// one PresenceSnapshot per workspace on TopicPresence. Desktop notifications
// are a client-local effect: the core never shows one, and a shell shows one
// only when this snapshot names it.
const (
	ShellPresenceMethod = "shell.presence"
	TopicPresence       = "state.presence"
)

// ShellPresenceReport is one shell's state. Present means that the shell's
// terminal has focus and its user acted recently. Notifies means that the
// shell can show desktop notifications and the user did not turn them off.
type ShellPresenceReport struct {
	Present  bool `json:"present"`
	Notifies bool `json:"notifies"`
}

// PresenceSnapshot is the workspace's presence. Present means that the user
// is present in at least one attached shell; then no shell notifies. Else
// Notifier is the shell that notifies: of the shells that can, the one whose
// user was present last. A zero Notifier means that no shell notifies.
type PresenceSnapshot struct {
	Present  bool  `json:"present"`
	Notifier Owner `json:"notifier"`
}
