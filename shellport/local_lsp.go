package shellport

import (
	"context"
	"crude/workspace"
	"fmt"
	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
)

// localLSP adapts the legacy client rename union only at the local boundary.
// It does not change client fakes or drain the shared manager's diagnostics.
type localLSP struct{ *workspace.LSPManager }

var _ LSPService = (*localLSP)(nil)

func (l *localLSP) RequestRename(ctx context.Context, path string, line, col int, name string) (*protocol.WorkspaceEdit, error) {
	value, err := l.LSPManager.RequestRename(ctx, path, line, col, name)
	if err != nil || value == nil {
		return nil, err
	}
	switch edit := value.(type) {
	case *protocol.WorkspaceEdit:
		return edit, nil
	case protocol.WorkspaceEdit:
		return &edit, nil
	default:
		return nil, fmt.Errorf("unexpected LSP rename result %T", value)
	}
}

func (l *localLSP) DiagnosticSnapshot() LSPDiagnosticSnapshot {
	// Manager notifications may race a read: only return a version which preceded
	// these contents so the next tick always notices any concurrent publication.
	version := l.Version()
	return LSPDiagnosticSnapshot{Version: version, Diagnostics: l.AllDiagnostics()}
}
