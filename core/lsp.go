package core

import (
	"context"
	"crude/coreprotocol"
	"crude/shellport"
	"crude/types"
	"crude/workspace"
)

func lspMethod(method string) bool {
	switch method {
	case coreprotocol.LSPSnapshotMethod, coreprotocol.LSPNotifyWatchedFilesChanged,
		coreprotocol.LSPSetActiveFile, coreprotocol.LSPStartForWorkspace, coreprotocol.LSPStop,
		coreprotocol.LSPRestartServer, coreprotocol.LSPStopServer, coreprotocol.LSPFindReferences,
		coreprotocol.LSPRequestCompletions, coreprotocol.LSPRequestDefinition,
		coreprotocol.LSPRequestDocumentSymbols, coreprotocol.LSPRequestHover, coreprotocol.LSPRequestRename,
		coreprotocol.LSPRequestSignatureHelp:
		return true
	}
	return false
}

// prepareLSP captures the core-owned service on the actor. All requests, including
// snapshot assembly, run on a resource worker; no RPC probes a root or pipe here.
func (a *actor) prepareLSP(request coreprotocol.Request) (func(context.Context) coreprotocol.Response, bool) {
	if !lspMethod(request.Method) {
		return nil, false
	}
	var in coreprotocol.LSPRequest
	if err := decodeRequest(request, &in); err != nil {
		return func(context.Context) coreprotocol.Response { return fault(request, "invalid_request", err.Error()) }, true
	}
	manager := a.ws.LSPMgr
	service := shellport.Local(a.ws).LSPMgr()
	root := a.ws.RootPath
	documents := a.ws.LiveDocuments
	return func(ctx context.Context) coreprotocol.Response {
		if err := ctx.Err(); err != nil {
			return fault(request, "cancelled", err.Error())
		}
		if request.Method == coreprotocol.LSPSnapshotMethod {
			return response(request, lspSnapshot(manager))
		}
		if service == nil {
			return fault(request, "lsp_unavailable", "workspace language service is unavailable")
		}
		if request.Method == coreprotocol.LSPStartForWorkspace && in.Path != "" && in.Path != root {
			return fault(request, "invalid_request", "LSP root must match attached workspace")
		}
		if request.Method == coreprotocol.LSPNotifyWatchedFilesChanged && documents != nil {
			if err := documents.ReconcileDisk(ctx, in.Path); err != nil {
				return fault(request, "lsp_failed", err.Error())
			}
		}
		result, err := runLSP(ctx, service, request.Method, in, root)
		if err != nil {
			return fault(request, "lsp_failed", err.Error())
		}
		return response(request, result)
	}, true
}

func runLSP(ctx context.Context, service shellport.LSPService, method string, in coreprotocol.LSPRequest, root string) (coreprotocol.LSPResult, error) {
	out := coreprotocol.LSPResult{Applied: true}
	var err error
	switch method {
	case coreprotocol.LSPNotifyWatchedFilesChanged:
		service.NotifyWatchedFilesChanged(ctx, in.Path, in.ChangeType)
	case coreprotocol.LSPSetActiveFile:
		service.SetActiveFile(in.Path)
	case coreprotocol.LSPStartForWorkspace:
		service.StartForWorkspace(root)
	case coreprotocol.LSPStop:
		service.Stop()
	case coreprotocol.LSPRestartServer:
		out.Applied = service.RestartServer(in.ServerID)
	case coreprotocol.LSPStopServer:
		out.Applied = service.StopServer(in.ServerID)
	case coreprotocol.LSPFindReferences:
		out.Locations = service.FindReferences(ctx, in.Path, in.Line, in.Col, in.IncludeDeclaration)
	case coreprotocol.LSPRequestCompletions:
		out.Completions = service.RequestCompletions(ctx, in.Path, in.Line, in.Col)
	case coreprotocol.LSPRequestDefinition:
		out.Locations = service.RequestDefinition(ctx, in.Path, in.Line, in.Col)
	case coreprotocol.LSPRequestDocumentSymbols:
		out.Symbols = service.RequestDocumentSymbols(ctx, in.Path)
	case coreprotocol.LSPRequestHover:
		out.Hover, err = service.RequestHover(ctx, in.Path, in.Line, in.Col)
	case coreprotocol.LSPRequestSignatureHelp:
		out.SignatureHelp, err = service.RequestSignatureHelp(ctx, in.Path, in.Line, in.Col)
	case coreprotocol.LSPRequestRename:
		edit, renameErr := service.RequestRename(ctx, in.Path, in.Line, in.Col, in.NewName)
		out.Edit, err = (*coreprotocol.LSPEdit)(edit), renameErr
	}
	return out, err
}

func lspSnapshot(manager *workspace.LSPManager) coreprotocol.LSPSnapshot {
	if manager == nil {
		return coreprotocol.LSPSnapshot{}
	}
	// Version precedes contents so a concurrent publication cannot be hidden:
	// the owner broadcast announces it and the client reads a newer complete snapshot.
	generation, version := manager.DiagnosticCursor()
	out := coreprotocol.LSPSnapshot{Available: true, Generation: generation, Version: version}
	_, out.ContextGeneration = manager.ContextCursor()
	out.Diagnostics = manager.AllDiagnostics()
	out.Symbols = make(map[string][]workspace.LSPSymbol)
	for path, symbols := range manager.AllDocumentSymbols() {
		out.Symbols[path] = cloneLSPSymbols(symbols)
	}
	out.Insights, out.Health = manager.ProjectInsights(), manager.ServerHealthSnapshots()
	out.Settings = types.CloneLSPSettings(manager.Settings())
	out.Connected, out.Status, out.Error = manager.Status()
	out.Ready = manager.ServerReady()
	out.ShowInEditor, out.ShowInTree = manager.ShowDiagnosticsInEditor(), manager.ShowDiagnosticsInTree()
	return out
}

func cloneLSPSymbols(in []workspace.LSPSymbol) []workspace.LSPSymbol {
	out := append([]workspace.LSPSymbol(nil), in...)
	for i := range out {
		out[i].Children = cloneLSPSymbols(out[i].Children)
	}
	return out
}

func (a *actor) publishLSPIfChanged() {
	if a.ws.LSPMgr == nil {
		return
	}
	if version := a.ws.LSPMgr.Version(); version != a.lspVersion {
		a.lspVersion = version
		a.broadcast(coreprotocol.Change{LSP: true})
	}
}
