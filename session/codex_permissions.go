package session

import (
	"crude/codexio"
	"crude/safety"
)

func (s *Session) patchAlwaysAllowed(input string) bool {
	if s.permStore == nil {
		return false
	}
	patch, err := codexio.Parse(input)
	if err != nil || len(patch.Operations) == 0 {
		return false
	}
	for _, access := range patch.Accesses(s.WorkDir()) {
		if !s.permStore.IsAlwaysAllowed(safety.CRUDEOp(access.Operation), "apply_patch", access.Path) {
			return false
		}
	}
	return true
}

func (s *Session) grantPatchAlways(input string) {
	if s.permStore == nil {
		return
	}
	patch, err := codexio.Parse(input)
	if err != nil {
		return
	}
	for _, access := range patch.Accesses(s.WorkDir()) {
		s.permStore.GrantAlways(safety.CRUDEOp(access.Operation), "apply_patch", access.Path)
	}
}
