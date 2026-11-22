package shellport

import (
	"context"
	"reflect"

	"crude/types/insight"
	"crude/types/setting"
	"crude/types/statuscenter"
)

// LSPService is the workspace language-server manager.
type LSPService interface {
	// AllDiagnostics returns a snapshot of every cached diagnostic across all
	// files the LSP server has reported on. The returned map is a copy; mutating
	// it does not affect internal state. Used by the status bar's "Goto first
	// error" action and similar cross-file navigation features.
	AllDiagnostics() map[string]insight.Diagnostics
	// DocumentSymbols returns the cached document outline for a file,
	// if any. Callers should not assume the cache is fresh; refresh via
	// RequestDocumentSymbols when the file's content has changed materially.
	DocumentSymbols(filePath string) []insight.LSPSymbol
	// DiagnosticSnapshot is a non-destructive, versioned read for each shell.
	DiagnosticSnapshot() LSPDiagnosticSnapshot
	DiagnosticCursor() (generation string, version uint64)
	ContextCursor() (generation string, context uint64)
	InlayHintsState() (enabled bool, generation uint64)
	CompletionsState() (enabled bool, generation uint64)
	// ReferencesEnabled is a constant-size feature-gate read for passive
	// occurrences; unlike Settings, it does not clone the language catalog.
	ReferencesEnabled() bool
	AssistanceCapabilities(path string) insight.LSPAssistanceCapabilities
	// FileProducer is the current connected producer for this path, or zero.
	// It is a small snapshot read and does not copy trigger lists.
	FileProducer(path string) insight.Producer
	// FileSynchronized reads the exact file/source/context admission snapshot.
	FileSynchronized(source insight.Source, context uint64) bool
	FileDiagnostics(filePath string) (insight.Diagnostics, bool)
	// Watchers report disk events, never read contents or take ownership of editor
	// overlays. Include manifests/configuration, not just known source extensions.
	NotifyWatchedFilesChanged(ctx context.Context, path string, changeType int)
	// ProjectInsights combines producer health with qualified observed-unit
	// counts. It is not an exhaustive project check or coverage denominator.
	ProjectInsights() insight.ProjectInsights
	// RequestDocumentSymbols routes a "document symbols" request to the right
	// client and caches the result for the document outline panel.
	RequestDocumentSymbols(ctx context.Context, filePath string) []insight.LSPSymbol
	// RequestHover routes a hover request to the right client.
	RequestHover(ctx context.Context, filePath string, line, col int) (string, error)
	RequestSignatureHelp(ctx context.Context, filePath string, line, col int, trigger insight.LSPSignatureContext) (insight.LSPSignatureHelp, error)
	RestartServer(id string) bool
	// ServerHealthSnapshots exposes every configured/attempted server separately.
	// Diagnostic counts remain project-scoped until the cache records server
	// origin.
	ServerHealthSnapshots() []statuscenter.LSPServerHealth
	// Assembly is the project's code-intelligence plan: languages found,
	// the server for each, where it comes from and what it checks.
	Assembly() insight.AssemblyPlan
	// ServerReady reports the selected status, normally for the active file.
	// Without an active document it can fall back to another server. Use
	// FileProducer for an exact file.
	ServerReady() bool
	SetActiveFile(path string)
	Settings() setting.LSP
	ShowDiagnosticsInEditor() bool
	ShowDiagnosticsInTree() bool
	// StartForWorkspace explicitly resumes intelligence for retained editor
	// buffers. It never fabricates didOpen messages for the contents of the
	// workspace.
	StartForWorkspace(root string)
	Status() (connected bool, status insight.LSPStatus, errMsg string)
	// Stop is safe on Update: admission stops immediately; the worker owns
	// teardown.
	Stop()
	// Refresh is the user's explicit re-check: every server restarts and the
	// stopped servers' results for files no editor holds are removed.
	Refresh(root string)
	StopServer(id string) bool
	Version() uint64
}

// LSPDiagnosticSnapshot is detached and complete; absent paths mean cleared diagnostics.
type LSPDiagnosticSnapshot struct {
	Generation  string
	Version     uint64
	Diagnostics map[string]insight.Diagnostics
}

// LSPDiagnosticConsumer retains a cursor per shell, not per shared manager.
// A complete snapshot allows clears and resynchronization without destructive drains.
type LSPDiagnosticConsumer struct {
	initialized bool
	generation  string
	version     uint64
	previous    map[string]insight.Diagnostics
}

func (c *LSPDiagnosticConsumer) NeedsSnapshot(generation string, version uint64) bool {
	return !c.initialized || c.generation != generation || c.version != version
}

// Consume returns the diagnostics that changed since the previous call.
func (c *LSPDiagnosticConsumer) Consume(s LSPDiagnosticSnapshot) map[string]insight.Diagnostics {
	if c.initialized && s.Generation == c.generation && s.Version == c.version {
		return nil
	}
	out := make(map[string]insight.Diagnostics)
	for path, ds := range s.Diagnostics {
		if !c.initialized || !reflect.DeepEqual(ds, c.previous[path]) {
			out[path] = insight.CloneDiagnostics(ds)
		}
	}
	for path := range c.previous {
		if _, ok := s.Diagnostics[path]; !ok {
			out[path] = insight.Diagnostics{}
		}
	}
	c.previous = make(map[string]insight.Diagnostics, len(s.Diagnostics))
	for path, ds := range s.Diagnostics {
		c.previous[path] = insight.CloneDiagnostics(ds)
	}
	c.initialized, c.generation, c.version = true, s.Generation, s.Version
	return out
}
