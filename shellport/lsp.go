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
	AllDiagnostics() map[string][]workspace.LSPDiagnostic
	// ClearDiagnostics removes all cached diagnostics, document symbols, per-file
	// sync timestamps, and the top-issue tally. Call this when a server restart
	// should produce a clean slate rather than showing stale results from the
	// previous session.
	ClearDiagnostics()
	CloseFile(_ context.Context, path string)
	// DocumentSymbols returns the cached document outline for a file,
	// if any. Callers should not assume the cache is fresh; refresh via
	// RequestDocumentSymbols when the file's content has changed materially.
	DocumentSymbols(filePath string) []workspace.LSPSymbol
	// DiagnosticSnapshot is a non-destructive, versioned read for each shell.
	DiagnosticSnapshot() LSPDiagnosticSnapshot
	FileDiagnostics(filePath string) []workspace.LSPDiagnostic
	// FindReferences routes a "find references" request to the right client.
	FindReferences(ctx context.Context, filePath string, line, col int, includeDeclaration bool) []workspace.LSPLocation
	// Watchers report disk events, never read contents or take ownership of editor
	// overlays. Include manifests/configuration, not just known source extensions.
	NotifyWatchedFilesChanged(ctx context.Context, path string, changeType int)
	// OpenFile registers editor intent synchronously; no root probing or pipe I/O
	// runs here. Closing before initialization completes prevents a late didOpen.
	OpenFile(_ context.Context, path, text string)
	// ProjectInsights returns a whole-project snapshot suitable for the
	// bottom-left status bar. Always reflects the state of every file the LSP
	// server knows about, regardless of which file (if any) is open in the editor.
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
	Version     uint64
	Diagnostics map[string][]workspace.LSPDiagnostic
}

// LSPDiagnosticConsumer retains a cursor per shell, not per shared manager.
// A complete snapshot allows clears and resynchronization without destructive drains.
type LSPDiagnosticConsumer struct {
	initialized bool
	version     uint64
	previous    map[string][]workspace.LSPDiagnostic
}

func (c *LSPDiagnosticConsumer) Consume(s LSPDiagnosticSnapshot) map[string][]workspace.LSPDiagnostic {
	if c.initialized && s.Version == c.version {
		return nil
	}
	out := make(map[string][]workspace.LSPDiagnostic)
	for path, ds := range s.Diagnostics {
		if !c.initialized || !reflect.DeepEqual(ds, c.previous[path]) {
			out[path] = append([]workspace.LSPDiagnostic(nil), ds...)
		}
	}
	for path := range c.previous {
		if _, ok := s.Diagnostics[path]; !ok {
			out[path] = nil
		}
	}
	c.previous = make(map[string][]workspace.LSPDiagnostic, len(s.Diagnostics))
	for path, ds := range s.Diagnostics {
		c.previous[path] = append([]workspace.LSPDiagnostic(nil), ds...)
	}
	c.initialized, c.version = true, s.Version
	return out
}
