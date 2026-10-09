package core

import (
	"context"
	"crude/coreprotocol"
	"crude/document"
	"crude/shellport"
	"crude/types"
	"crude/workspace"
	"errors"
)

func lspMethod(method string) bool {
	switch method {
	case coreprotocol.LSPSnapshotMethod, coreprotocol.LSPClearDiagnostics,
		coreprotocol.LSPOpenFile, coreprotocol.LSPCloseFile, coreprotocol.LSPQueueFileUpdate,
		coreprotocol.LSPNotifyFileSave, coreprotocol.LSPNotifyWatchedFilesChanged,
		coreprotocol.LSPSetActiveFile, coreprotocol.LSPStartForWorkspace, coreprotocol.LSPStop,
		coreprotocol.LSPRestartServer, coreprotocol.LSPStopServer, coreprotocol.LSPFindReferences,
		coreprotocol.LSPRequestCompletions, coreprotocol.LSPRequestDefinition,
		coreprotocol.LSPRequestDocumentSymbols, coreprotocol.LSPRequestHover, coreprotocol.LSPRequestRename:
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
	if err := decode(request.Payload, &in); err != nil {
		return func(context.Context) coreprotocol.Response { return fault(request, "invalid_request", err.Error()) }, true
	}
	manager := a.ws.LSPMgr
	service := shellport.Local(a.ws).LSPMgr()
	root := a.ws.RootPath
	views := newLSPViews(a.ws.LiveDocuments)
	owner := document.Owner{ClientID: request.Owner.ClientID, Epoch: request.Owner.Epoch}
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
		var viewErr error
		switch request.Method {
		case coreprotocol.LSPOpenFile:
			viewErr = views.Open(owner, in.Path)
		case coreprotocol.LSPCloseFile:
			viewErr = views.Close(owner, root, in.Path)
		case coreprotocol.LSPQueueFileUpdate, coreprotocol.LSPNotifyFileSave:
			viewErr = errors.New("legacy LSP text writes are disabled; use revision-fenced document mutations")
		case coreprotocol.LSPNotifyWatchedFilesChanged:
			viewErr = views.Reconcile(ctx, root, in.Path)
		default:
			goto query
		}
		if viewErr != nil {
			return fault(request, "lsp_failed", viewErr.Error())
		}
		return response(request, coreprotocol.LSPResult{Applied: true})
	query:
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
	case coreprotocol.LSPClearDiagnostics:
		service.ClearDiagnostics()
	case coreprotocol.LSPOpenFile:
		service.OpenFile(ctx, in.Path, in.Text)
	case coreprotocol.LSPCloseFile:
		service.CloseFile(ctx, in.Path)
	case coreprotocol.LSPQueueFileUpdate:
		service.QueueFileUpdate(in.Path, in.Text)
	case coreprotocol.LSPNotifyFileSave:
		service.NotifyFileSave(ctx, in.Path, in.Text, in.Reason, in.Before)
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
	case coreprotocol.LSPRequestRename:
		out.Edit, err = service.RequestRename(ctx, in.Path, in.Line, in.Col, in.NewName)
	}
	return out, err
}

func lspSnapshot(manager *workspace.LSPManager) coreprotocol.LSPSnapshot {
	if manager == nil {
		return coreprotocol.LSPSnapshot{}
	}
	// Version precedes contents so a concurrent publication cannot be hidden:
	// next actor tick announces it and the client reads a newer complete snapshot.
	out := coreprotocol.LSPSnapshot{Available: true, Version: manager.Version()}
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
