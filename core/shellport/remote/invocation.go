package remote

import (
	"crude/workspace"
)

// ConfigureInvocation is called once at shell startup, before lifecycle commands
// or replica delivery. Selection is attachment-local, never core defaults.
func (w *Workspace) ConfigureInvocation(selection workspace.InvocationSelection) {
	w.invocation = selection
}
