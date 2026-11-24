package remote

import (
	"context"
	"crude/core/coreclient"
	"crude/core/coreprotocol"
	"crude/core/shellport"
	"crude/types/insight"
	"crude/types/setting"
	"crude/types/statuscenter"
	"errors"
	"path/filepath"
	"slices"
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
	err := l.w.call(ctx, coreprotocol.LSPSnapshotMethod, &coreprotocol.LSPRequest{}, &out)
	return out, err
}

// install runs only on Update. The shell's diagnostic cursor reads this version
// on its next tick, including removed paths. Reconnect resync may reset versions.
func (l *lspReplica) install(snapshot coreprotocol.LSPSnapshot) []any {
	l.snapshot, l.installed = cloneLSPSnapshot(snapshot), true
	return nil
}

func cloneLSPSymbols(in []insight.LSPSymbol) []insight.LSPSymbol {
	out := append([]insight.LSPSymbol(nil), in...)
	for i := range out {
		out[i].Children = cloneLSPSymbols(out[i].Children)
	}
	return out
}
func cloneLSPDiagnostics(in map[string]insight.Diagnostics) map[string]insight.Diagnostics {
	out := make(map[string]insight.Diagnostics, len(in))
	for path, ds := range in {
		out[path] = insight.CloneDiagnostics(ds)
	}
	return out
}
func cloneLSPSnapshot(in coreprotocol.LSPSnapshot) coreprotocol.LSPSnapshot {
	assistance := make(map[string]insight.LSPAssistanceCapabilities, len(in.Assistance))
	for path, caps := range in.Assistance {
		assistance[path] = insight.CloneLSPAssistance(caps)
	}
	in.Assistance = assistance
	in.Diagnostics = cloneLSPDiagnostics(in.Diagnostics)
	syms := make(map[string][]insight.LSPSymbol, len(in.Symbols))
	for path, ss := range in.Symbols {
		syms[path] = cloneLSPSymbols(ss)
	}
	in.Symbols = syms
	in.Health = append([]statuscenter.LSPServerHealth(nil), in.Health...)
	in.Insights.Analysis = slices.Clone(in.Insights.Analysis)
	in.Assembly = insight.CloneAssemblyPlan(in.Assembly)
	in.Settings = setting.CloneLSP(in.Settings)
	return in
}

func (l *lspReplica) send(method string, input coreprotocol.LSPRequest) {
	l.w.services.push(func(ctx context.Context) {
		var out coreprotocol.LSPResult
		if err := l.w.call(ctx, method, &input, &out); err != nil {
			l.w.logger.Warn("REMOTE", "lsp_mutation_failed", "method", method, "error", err.Error())
		}
	})
}
func (l *lspReplica) query(ctx context.Context, method string, input coreprotocol.LSPRequest) (coreprotocol.LSPResult, error) {
	var out coreprotocol.LSPResult
	err := l.w.callCancellable(ctx, method, &input, &out)
	return out, err
}

// Semantic reads, reviewed edits and safety disposition share the exact connected
// cancellation/join owner. A cleanup error is returned with any known result,
// never converted into a fresh request or a second exchange implementation.
func (w *Workspace) callCancellable(ctx context.Context, method string, input coreprotocol.Value, out coreprotocol.Target) error {
	return w.callCancellableWithProgress(ctx, method, input, out, nil)
}

func (w *Workspace) callCancellableWithProgress(ctx context.Context, method string, input coreprotocol.Value, out coreprotocol.Target, progress func(string)) error {
	client, id := w.conn()
	if client == nil {
		return errDisconnected
	}
	// The caller's own deadline wins: a search that is allowed two minutes
	// must not die at the generic call cap.
	var cancel context.CancelFunc
	if _, bounded := ctx.Deadline(); bounded {
		ctx, cancel = context.WithCancel(ctx)
	} else {
		ctx, cancel = context.WithTimeout(ctx, callTimeout)
	}
	defer cancel()
	cancelled := make(chan struct{})
	stop := context.AfterFunc(w.ctx, func() {
		defer close(cancelled)
		cancel()
	})
	defer func() {
		if !stop() {
			<-cancelled
		}
	}()
	err := client.CallCancellableWithProgress(ctx, id, method, input, out, progress)
	var uncertain *coreclient.UncertainError
	if errors.As(err, &uncertain) {
		// Some older slice-only port methods cannot return errors. Preserve the
		// exact cleanup identity here without logging request/source contents.
		w.logger.Warn("REMOTE", "connected_call_cleanup_unconfirmed", "method", method,
			"request_id", uncertain.RequestID, "outcome_state", uncertain.State, "result_known", uncertain.ResultKnown)
	}
	return err
}

func (l *lspReplica) AllDiagnostics() map[string]insight.Diagnostics {
	return cloneLSPDiagnostics(l.snapshot.Diagnostics)
}
func (l *lspReplica) DiagnosticSnapshot() shellport.LSPDiagnosticSnapshot {
	return shellport.LSPDiagnosticSnapshot{Generation: l.snapshot.Generation, Version: l.snapshot.DiagnosticVersion, Diagnostics: l.AllDiagnostics()}
}
func (l *lspReplica) DiagnosticCursor() (string, uint64) {
	return l.snapshot.Generation, l.snapshot.DiagnosticVersion
}
func (l *lspReplica) ContextCursor(path string) (string, uint64) {
	if caps, ok := l.snapshot.Assistance[filepath.Clean(path)]; ok && caps.ContextGeneration != 0 {
		return l.snapshot.Generation, caps.ContextGeneration
	}
	return l.snapshot.Generation, l.snapshot.ContextGeneration
}
func (l *lspReplica) InlayHintsState() (bool, uint64) {
	return l.snapshot.ShowInlays, l.snapshot.InlayGeneration
}
func (l *lspReplica) CompletionsState() (bool, uint64) {
	return l.snapshot.ShowCompletions, l.snapshot.CompletionGeneration
}
func (l *lspReplica) ReferencesEnabled() bool {
	return l.snapshot.Settings.Enabled && l.snapshot.Settings.References
}
func (l *lspReplica) FileDiagnostics(path string) (insight.Diagnostics, bool) {
	batch, ok := l.snapshot.Diagnostics[filepath.Clean(path)]
	return insight.CloneDiagnostics(batch), ok
}
func (l *lspReplica) DocumentSymbols(path string) []insight.LSPSymbol {
	return cloneLSPSymbols(l.snapshot.Symbols[filepath.Clean(path)])
}
func (l *lspReplica) ProjectInsights() insight.ProjectInsights {
	s := l.snapshot.Insights
	s.Analysis = slices.Clone(s.Analysis)
	return s
}
func (l *lspReplica) ServerHealthSnapshots() []statuscenter.LSPServerHealth {
	return append([]statuscenter.LSPServerHealth(nil), l.snapshot.Health...)
}
func (l *lspReplica) Assembly() insight.AssemblyPlan {
	return insight.CloneAssemblyPlan(l.snapshot.Assembly)
}
func (l *lspReplica) Settings() setting.LSP         { return setting.CloneLSP(l.snapshot.Settings) }
func (l *lspReplica) ShowDiagnosticsInEditor() bool { return l.snapshot.ShowInEditor }
func (l *lspReplica) ShowDiagnosticsInTree() bool   { return l.snapshot.ShowInTree }
func (l *lspReplica) ServerReady() bool             { return l.snapshot.Ready }
func (l *lspReplica) Status() (bool, insight.LSPStatus, string) {
	return l.snapshot.Connected, l.snapshot.Status, l.snapshot.Error
}
func (l *lspReplica) Version() uint64 { return l.snapshot.Version }
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
func (l *lspReplica) Refresh(root string) {
	l.send(coreprotocol.LSPRefresh, coreprotocol.LSPRequest{Path: root})
}

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
func (l *lspReplica) RequestDocumentSymbols(ctx context.Context, path string) []insight.LSPSymbol {
	out, _ := l.query(ctx, coreprotocol.LSPRequestDocumentSymbols, coreprotocol.LSPRequest{Path: path})
	return out.Symbols
}
func (l *lspReplica) RequestHover(ctx context.Context, path string, line, col int) (string, error) {
	out, err := l.query(ctx, coreprotocol.LSPRequestHover, coreprotocol.LSPRequest{Path: path, Line: line, Col: col})
	return out.Hover, err
}
func (l *lspReplica) RequestSignatureHelp(ctx context.Context, path string, line, col int, trigger insight.LSPSignatureContext) (insight.LSPSignatureHelp, error) {
	out, err := l.query(ctx, coreprotocol.LSPRequestSignatureHelp, coreprotocol.LSPRequest{Path: path, Line: line, Col: col, Trigger: trigger})
	return out.SignatureHelp, err
}

func (l *lspReplica) AssistanceCapabilities(path string) insight.LSPAssistanceCapabilities {
	return insight.CloneLSPAssistance(l.snapshot.Assistance[filepath.Clean(path)])
}

// FileProducer observes the same per-path snapshot as AssistanceCapabilities,
// without cloning its independent completion and signature trigger lists.
func (l *lspReplica) FileProducer(path string) insight.Producer {
	return l.snapshot.Assistance[filepath.Clean(path)].Producer
}

// FileSynchronized observes only the detached per-file synchronization snapshot.
func (l *lspReplica) FileSynchronized(source insight.Source, context uint64) bool {
	return l.snapshot.Assistance[filepath.Clean(source.Path)].MatchesSynchronizedSource(source, context)
}
