package remote

import "crude/coreprotocol"

// ConfigureInvocation is called once at shell startup, before lifecycle commands
// or replica delivery. Selection is attachment-local, never core defaults.
func (w *Workspace) ConfigureInvocation(selection coreprotocol.InvocationSelection) {
	w.invocation = selection
}
