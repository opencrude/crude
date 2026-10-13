package session

import (
	captypes "crude/types/capability"
	"crude/types/control"
)

// Lifecycle cleanup uses the same immutable task identity as request admission.
// In particular, a logical workspace or shell epoch is never reconstructed from
// whichever UI happens to be selected after the task settles.
func (s *Session) revokeCapabilityRunFor(actor captypes.RuntimeActor, target *control.Target) {
	if target != nil {
		actor.WorkspaceID = target.WorkspaceID
		if target.Shell != nil {
			shell := *target.Shell
			actor.ShellOwner = &shell
		}
	}
	s.revokeCapabilityRun(actor)
}
