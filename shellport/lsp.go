package shellport

import (
	"context"
	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
	"reflect"

	"crude/types"
	"crude/workspace"
)

// LSPService is the workspace language-server manager.
type LSPService interface {
	// AllDiagnostics returns a snapshot of every cached diagnostic across all
	// files the LSP server has reported on. The returned map is a copy; mutating
	// it does not affect internal state. Used by the status bar's "Goto first
	// error" action and similar cross-file navigation features.
	AllDiagnostics() map[string]types.InsightDiagnostics
	// DocumentSymbols returns the cached document outline for a file,
	// if any. Callers should not assume the cache is fresh; refresh via
	// RequestDocumentSymbols when the file's content has changed materially.
	DocumentSymbols(filePath string) []workspace.LSPSymbol
	// DiagnosticSnapshot is a non-destructive, versioned read for each shell.
	DiagnosticSnapshot() LSPDiagnosticSnapshot
	DiagnosticCursor() (generation string, version uint64)
	FileDiagnostics(filePath string) (types.InsightDiagnostics, bool)
	// FindReferences routes a "find references" request to the right client.
	FindReferences(ctx context.Context, filePath string, line, col int, includeDeclaration bool) []workspace.LSPLocation
	// Watchers report disk events, never read contents or take ownership of editor
	// overlays. Include manifests/configuration, not just known source extensions.
	NotifyWatchedFilesChanged(ctx context.Context, path string, changeType int)
	// ProjectInsights combines producer health with qualified observed-unit
	// counts. It is not an exhaustive project check or coverage denominator.
	ProjectInsights() workspace.ProjectInsights
	// RequestCompletions routes a completion request to the right client.
	RequestCompletions(ctx context.Context, filePath string, line, col int) []workspace.CompletionItem
	// RequestDefinition routes a "go to definition" request to the right client.
	RequestDefinition(ctx context.Context, filePath string, line, col int) []workspace.LSPLocation
	// RequestDocumentSymbols routes a "document symbols" request to the right
	// client and caches the result for the document outline panel.
	RequestDocumentSymbols(ctx context.Context, filePath string) []workspace.LSPSymbol
	// RequestHover routes a hover request to the right client.
	RequestHover(ctx context.Context, filePath string, line, col int) (string, error)
	// RequestRename routes a rename request to the right client.
	RequestRename(ctx context.Context, filePath string, line, col int, newName string) (*protocol.WorkspaceEdit, error)
	RestartServer(id string) bool
	// ServerHealthSnapshots exposes every configured/attempted server separately.
	// Diagnostic counts remain project-scoped until the cache records server
	// origin.
	ServerHealthSnapshots() []types.LSPServerHealth
	// ServerReady returns true when at least one LSP client has reached the
	// connected state. Used by the right-click context menu to enable LSP-backed
	// items.
	ServerReady() bool
	SetActiveFile(path string)
	Settings() types.LSPSettings
	ShowDiagnosticsInEditor() bool
	ShowDiagnosticsInTree() bool
	// StartForWorkspace explicitly resumes intelligence for retained editor
	// buffers. It never fabricates didOpen messages for the contents of the
	// workspace.
	StartForWorkspace(root string)
	Status() (connected bool, status workspace.LSPStatus, errMsg string)
	// Stop is safe on Update: admission stops immediately; the worker owns
	// teardown.
	Stop()
	StopServer(id string) bool
	Version() uint64
}

// LSPDiagnosticSnapshot is detached and complete; absent paths mean cleared diagnostics.
type LSPDiagnosticSnapshot struct {
	Generation  string
	Version     uint64
	Diagnostics map[string]types.InsightDiagnostics
}

// LSPDiagnosticConsumer retains a cursor per shell, not per shared manager.
// A complete snapshot allows clears and resynchronization without destructive drains.
type LSPDiagnosticConsumer struct {
	initialized bool
	generation  string
	version     uint64
	previous    map[string]types.InsightDiagnostics
}

func (c *LSPDiagnosticConsumer) NeedsSnapshot(generation string, version uint64) bool {
	return !c.initialized || c.generation != generation || c.version != version
}

func (c *LSPDiagnosticConsumer) Consume(s LSPDiagnosticSnapshot) map[string]types.InsightDiagnostics {
	if c.initialized && s.Generation == c.generation && s.Version == c.version {
		return nil
	}
	out := make(map[string]types.InsightDiagnostics)
	for path, ds := range s.Diagnostics {
		if !c.initialized || !reflect.DeepEqual(ds, c.previous[path]) {
			out[path] = types.CloneInsightDiagnostics(ds)
		}
	}
	for path := range c.previous {
		if _, ok := s.Diagnostics[path]; !ok {
			out[path] = types.InsightDiagnostics{}
		}
	}
	c.previous = make(map[string]types.InsightDiagnostics, len(s.Diagnostics))
	for path, ds := range s.Diagnostics {
		c.previous[path] = types.CloneInsightDiagnostics(ds)
	}
	c.initialized, c.generation, c.version = true, s.Generation, s.Version
	return out
}
