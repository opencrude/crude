package shellport

import "crude/workspace"

// localLSP adds the versioned diagnostic read to the in-process manager. It
// does not change client fakes or drain the shared manager's diagnostics.
type localLSP struct{ *workspace.LSPManager }

var _ LSPService = (*localLSP)(nil)

func (l *localLSP) DiagnosticSnapshot() LSPDiagnosticSnapshot {
	// Manager notifications may race a read: only return a version which preceded
	// these contents so the next tick always notices any concurrent publication.
	version := l.Version()
	return LSPDiagnosticSnapshot{Version: version, Diagnostics: l.AllDiagnostics()}
}
