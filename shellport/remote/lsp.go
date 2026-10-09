package remote

import (
	"context"
	"crude/coreprotocol"
	"crude/shellport"
	"crude/types"
	"crude/workspace"
	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
	"path/filepath"
)

// lspReplica owns only detached observations. Core alone owns server lifetime;
// attaching, installing and detaching this replica never starts or stops LSP.
type lspReplica struct {
	w         *Workspace
	snapshot  coreprotocol.LSPSnapshot
	installed bool
}

var _ shellport.LSPService = (*lspReplica)(nil)

func newLSPReplica(w *Workspace) *lspReplica      { return &lspReplica{w: w} }
func (w *Workspace) LSPMgr() shellport.LSPService { return w.lsp }

func (l *lspReplica) fetch(ctx context.Context) (coreprotocol.LSPSnapshot, error) {
	var out coreprotocol.LSPSnapshot
	err := l.w.call(ctx, coreprotocol.LSPSnapshotMethod, coreprotocol.LSPRequest{}, &out)
	return out, err
}

// install runs only on Update. The shell's diagnostic cursor reads this version
// on its next tick, including removed paths. Reconnect resync may reset versions.
func (l *lspReplica) install(snapshot coreprotocol.LSPSnapshot) []any {
	l.snapshot, l.installed = cloneLSPSnapshot(snapshot), true
	return nil
}

func cloneLSPSymbols(in []workspace.LSPSymbol) []workspace.LSPSymbol {
	out := append([]workspace.LSPSymbol(nil), in...)
	for i := range out {
		out[i].Children = cloneLSPSymbols(out[i].Children)
	}
	return out
}
func cloneLSPDiagnostics(in map[string][]workspace.LSPDiagnostic) map[string][]workspace.LSPDiagnostic {
	out := make(map[string][]workspace.LSPDiagnostic, len(in))
	for path, ds := range in {
		out[path] = append([]workspace.LSPDiagnostic(nil), ds...)
	}
	return out
}
func cloneLSPSnapshot(in coreprotocol.LSPSnapshot) coreprotocol.LSPSnapshot {
	in.Diagnostics = cloneLSPDiagnostics(in.Diagnostics)
	syms := make(map[string][]workspace.LSPSymbol, len(in.Symbols))
	for path, ss := range in.Symbols {
		syms[path] = cloneLSPSymbols(ss)
	}
	in.Symbols = syms
	in.Health = append([]types.LSPServerHealth(nil), in.Health...)
	in.Settings = types.CloneLSPSettings(in.Settings)
	return in
}

func (l *lspReplica) send(method string, input coreprotocol.LSPRequest) {
	l.w.commands.push(func(ctx context.Context) {
		var out coreprotocol.LSPResult
		if err := l.w.call(ctx, method, input, &out); err != nil {
			l.w.logger.Warn("REMOTE", "lsp_mutation_failed", "method", method, "error", err.Error())
		}
	})
}
func (l *lspReplica) query(ctx context.Context, method string, input coreprotocol.LSPRequest) (coreprotocol.LSPResult, error) {
	var out coreprotocol.LSPResult
	err := l.w.call(ctx, method, input, &out)
	return out, err
}

func (l *lspReplica) AllDiagnostics() map[string][]workspace.LSPDiagnostic {
	return cloneLSPDiagnostics(l.snapshot.Diagnostics)
}
func (l *lspReplica) DiagnosticSnapshot() shellport.LSPDiagnosticSnapshot {
	return shellport.LSPDiagnosticSnapshot{Version: l.snapshot.Version, Diagnostics: l.AllDiagnostics()}
}
func (l *lspReplica) FileDiagnostics(path string) []workspace.LSPDiagnostic {
	return append([]workspace.LSPDiagnostic(nil), l.snapshot.Diagnostics[filepath.Clean(path)]...)
}
func (l *lspReplica) DocumentSymbols(path string) []workspace.LSPSymbol {
	return cloneLSPSymbols(l.snapshot.Symbols[filepath.Clean(path)])
}
func (l *lspReplica) ProjectInsights() workspace.ProjectInsights { return l.snapshot.Insights }
func (l *lspReplica) ServerHealthSnapshots() []types.LSPServerHealth {
	return append([]types.LSPServerHealth(nil), l.snapshot.Health...)
}
func (l *lspReplica) Settings() types.LSPSettings   { return types.CloneLSPSettings(l.snapshot.Settings) }
func (l *lspReplica) ShowDiagnosticsInEditor() bool { return l.snapshot.ShowInEditor }
func (l *lspReplica) ShowDiagnosticsInTree() bool   { return l.snapshot.ShowInTree }
func (l *lspReplica) ServerReady() bool             { return l.snapshot.Ready }
func (l *lspReplica) Status() (bool, workspace.LSPStatus, string) {
	return l.snapshot.Connected, l.snapshot.Status, l.snapshot.Error
}
func (l *lspReplica) Version() uint64 { return l.snapshot.Version }
func (l *lspReplica) ClearDiagnostics() {
	l.send(coreprotocol.LSPClearDiagnostics, coreprotocol.LSPRequest{})
}
func (l *lspReplica) OpenFile(_ context.Context, path, text string) {
	l.send(coreprotocol.LSPOpenFile, coreprotocol.LSPRequest{Path: path, Text: text})
}
func (l *lspReplica) CloseFile(_ context.Context, path string) {
	l.send(coreprotocol.LSPCloseFile, coreprotocol.LSPRequest{Path: path})
}
func (l *lspReplica) QueueFileUpdate(path, text string) {
	l.send(coreprotocol.LSPQueueFileUpdate, coreprotocol.LSPRequest{Path: path, Text: text})
}
func (l *lspReplica) NotifyFileSave(_ context.Context, path, text string, reason workspace.DocumentSaveReason, before bool) {
	l.send(coreprotocol.LSPNotifyFileSave, coreprotocol.LSPRequest{Path: path, Text: text, Reason: reason, Before: before})
}
func (l *lspReplica) NotifyWatchedFilesChanged(_ context.Context, path string, changeType int) {
	l.send(coreprotocol.LSPNotifyWatchedFilesChanged, coreprotocol.LSPRequest{Path: path, ChangeType: changeType})
}
func (l *lspReplica) SetActiveFile(path string) {
	l.send(coreprotocol.LSPSetActiveFile, coreprotocol.LSPRequest{Path: path})
}
func (l *lspReplica) StartForWorkspace(root string) {
	l.send(coreprotocol.LSPStartForWorkspace, coreprotocol.LSPRequest{Path: root})
}
func (l *lspReplica) Stop() { l.send(coreprotocol.LSPStop, coreprotocol.LSPRequest{}) }

// Return indicates local admission for a known server, not a synchronous RPC
// acknowledgement. Core snapshots retain the authoritative resulting health.
func (l *lspReplica) hasServer(id string) bool {
	for _, server := range l.snapshot.Health {
		if server.ID == id {
			return true
		}
	}
	return false
}
func (l *lspReplica) RestartServer(id string) bool {
	if !l.hasServer(id) {
		return false
	}
	l.send(coreprotocol.LSPRestartServer, coreprotocol.LSPRequest{ServerID: id})
	return true
}
func (l *lspReplica) StopServer(id string) bool {
	if !l.hasServer(id) {
		return false
	}
	l.send(coreprotocol.LSPStopServer, coreprotocol.LSPRequest{ServerID: id})
	return true
}
func (l *lspReplica) FindReferences(ctx context.Context, path string, line, col int, include bool) []workspace.LSPLocation {
	out, _ := l.query(ctx, coreprotocol.LSPFindReferences, coreprotocol.LSPRequest{Path: path, Line: line, Col: col, IncludeDeclaration: include})
	return out.Locations
}
func (l *lspReplica) RequestDefinition(ctx context.Context, path string, line, col int) []workspace.LSPLocation {
	out, _ := l.query(ctx, coreprotocol.LSPRequestDefinition, coreprotocol.LSPRequest{Path: path, Line: line, Col: col})
	return out.Locations
}
func (l *lspReplica) RequestCompletions(ctx context.Context, path string, line, col int) []workspace.CompletionItem {
	out, _ := l.query(ctx, coreprotocol.LSPRequestCompletions, coreprotocol.LSPRequest{Path: path, Line: line, Col: col})
	return out.Completions
}
func (l *lspReplica) RequestDocumentSymbols(ctx context.Context, path string) []workspace.LSPSymbol {
	out, _ := l.query(ctx, coreprotocol.LSPRequestDocumentSymbols, coreprotocol.LSPRequest{Path: path})
	return out.Symbols
}
func (l *lspReplica) RequestHover(ctx context.Context, path string, line, col int) (string, error) {
	out, err := l.query(ctx, coreprotocol.LSPRequestHover, coreprotocol.LSPRequest{Path: path, Line: line, Col: col})
	return out.Hover, err
}
func (l *lspReplica) RequestRename(ctx context.Context, path string, line, col int, name string) (*protocol.WorkspaceEdit, error) {
	out, err := l.query(ctx, coreprotocol.LSPRequestRename, coreprotocol.LSPRequest{Path: path, Line: line, Col: col, NewName: name})
	return out.Edit, err
}
