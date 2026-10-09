package core

import (
	"context"
	"strings"
	"time"

	"crude/coreprotocol"
	"crude/types"
	"crude/workspace"
)

// prepareOwner serves owner operations whose exact target is resolved on the
// actor (where the local shell resolved it) but whose I/O runs on a worker.
func (a *actor) prepareOwner(request coreprotocol.Request) (func(context.Context) coreprotocol.Response, bool) {
	ws := a.ws
	bad := func(err error) (func(context.Context) coreprotocol.Response, bool) {
		return func(context.Context) coreprotocol.Response { return fault(request, "invalid_request", err.Error()) }, true
	}
	done := func(value any) (func(context.Context) coreprotocol.Response, bool) {
		return func(context.Context) coreprotocol.Response { return response(request, value) }, true
	}
	switch request.Method {
	case coreprotocol.AgentRecover, coreprotocol.AgentRecoverCancel:
		var in coreprotocol.RecoveryRequest
		if err := decode(request.Payload, &in); err != nil {
			return bad(err)
		}
		run := ws.PrepareAgentRecovery(in.AgentID, in.IncidentID, in.Generation)
		if request.Method == coreprotocol.AgentRecoverCancel {
			run = ws.PrepareCancelAgentRecovery(in.AgentID, in.IncidentID, in.Generation)
		}
		if run == nil {
			return done(coreprotocol.BoolResult{})
		}
		return func(context.Context) coreprotocol.Response {
			return response(request, coreprotocol.BoolResult{OK: run()})
		}, true
	case coreprotocol.AgentStopCommand:
		var in coreprotocol.StopCommandRequest
		if err := decode(request.Payload, &in); err != nil {
			return bad(err)
		}
		stop, err := ws.PrepareAsyncCommandTermination(in.AgentID, in.Ref)
		if err != nil {
			return done(coreprotocol.BoolResult{Error: err.Error()})
		}
		return func(context.Context) coreprotocol.Response {
			if err := stop(); err != nil {
				return response(request, coreprotocol.BoolResult{Error: err.Error()})
			}
			return response(request, coreprotocol.BoolResult{OK: true})
		}, true
	case coreprotocol.AgentCompact:
		var in coreprotocol.AgentIDRequest
		if err := decode(request.Payload, &in); err != nil {
			return bad(err)
		}
		if ws.AgentTurnActive(in.AgentID) {
			return done(types.CompactResultMsg{Error: "Stop the active turn before compacting context"})
		}
		prepared, result := ws.PrepareCompactForAgent(in.AgentID)
		if prepared.Run == nil {
			return done(result)
		}
		// The boundary row is reduced once here, so every shell shows it.
		if reduced := ws.ApplyAgentEvent(workspace.AgentEvent{Type: "context_compaction_started", AgentID: in.AgentID, Data: map[string]any{
			"request_id": prepared.OperationID, "trigger": "manual", "role": "root",
			"provider_id": prepared.ProviderID, "model_id": prepared.ModelID,
			"stage": 0, "completed_stages": 0, "stage_count": 4, "retry_limit": 3,
		}}); reduced.Accepted {
			a.reduced(reduced)
		}
		return func(context.Context) coreprotocol.Response { return response(request, prepared.Run()) }, true
	case coreprotocol.ToolLayoutLoad:
		store := &workspace.Workspace{WorkspaceDB: ws.WorkspaceDB}
		return func(context.Context) coreprotocol.Response {
			layout, err := store.LoadToolLayout()
			if err != nil {
				return fault(request, "load_failed", err.Error())
			}
			return response(request, coreprotocol.ToolLayoutRequest{Layout: layout})
		}, true
	case coreprotocol.ToolLayoutSave:
		var in coreprotocol.ToolLayoutRequest
		if err := decode(request.Payload, &in); err != nil {
			return bad(err)
		}
		store := &workspace.Workspace{WorkspaceDB: ws.WorkspaceDB}
		return func(context.Context) coreprotocol.Response {
			if err := store.SaveToolLayout(in.Layout); err != nil {
				return fault(request, "save_failed", err.Error())
			}
			return response(request, coreprotocol.BoolResult{OK: true})
		}, true
	case coreprotocol.HistoryRestoreEvent:
		var in coreprotocol.HistoryRestoreEventRequest
		if err := decode(request.Payload, &in); err != nil {
			return bad(err)
		}
		return func(context.Context) coreprotocol.Response {
			if err := ws.AppendHistoryRestoreEvent(in.Result); err != nil {
				return fault(request, "append_failed", err.Error())
			}
			return response(request, coreprotocol.BoolResult{OK: true})
		}, true
	case coreprotocol.HistoryUndoScopes:
		var in coreprotocol.UndoScopesRequest
		if err := decode(request.Payload, &in); err != nil {
			return bad(err)
		}
		service := ws.HistoryService
		return func(context.Context) coreprotocol.Response {
			if service == nil {
				return fault(request, "unavailable", "local history is unavailable")
			}
			scopes, err := service.AgentUndoableScopes(in.SessionID, in.AgentID)
			if err != nil {
				return fault(request, "history_failed", err.Error())
			}
			return response(request, coreprotocol.UndoScopesResult{Scopes: scopes})
		}, true
	case coreprotocol.GitStagePaths, coreprotocol.GitUnstagePaths:
		var in coreprotocol.GitPathsRequest
		if err := decode(request.Payload, &in); err != nil {
			return bad(err)
		}
		if ws.Git == nil {
			ws.Git = workspace.NewGitService(ws.RootPath)
		}
		git := ws.Git
		stage := request.Method == coreprotocol.GitStagePaths
		return func(ctx context.Context) coreprotocol.Response {
			var err error
			if stage {
				err = git.Stage(ctx, in.Paths...)
			} else {
				err = git.Unstage(ctx, in.Paths...)
			}
			if err != nil {
				return fault(request, "git_failed", err.Error())
			}
			return response(request, coreprotocol.BoolResult{OK: true})
		}, true
	case coreprotocol.GitCommitModel:
		writer, err := ws.PrepareCommitMessageWriter()
		if err != nil {
			return func(context.Context) coreprotocol.Response { return fault(request, "unavailable", err.Error()) }, true
		}
		return done(coreprotocol.CommitModelResult{Model: writer.Model})
	case coreprotocol.GitCommitMessage:
		var in coreprotocol.CommitMessageRequest
		if err := decode(request.Payload, &in); err != nil {
			return bad(err)
		}
		writer, err := ws.PrepareCommitMessageWriter()
		if err != nil {
			return func(context.Context) coreprotocol.Response { return fault(request, "unavailable", err.Error()) }, true
		}
		progress := a.progressSink(request)
		return func(ctx context.Context) coreprotocol.Response {
			message, err := writer.Write(ctx, in.StageAll, progress)
			if err != nil {
				return fault(request, "generation_failed", err.Error())
			}
			return response(request, coreprotocol.CommitMessageResult{Message: message})
		}, true
	}
	return nil, false
}

// ownerMutation reports which owner operations are journaled.
func ownerMutation(method string) bool {
	switch method {
	case coreprotocol.AgentRecover, coreprotocol.AgentRecoverCancel, coreprotocol.AgentStopCommand, coreprotocol.AgentCompact,
		coreprotocol.WorkspaceOverridesSet, coreprotocol.ToolLayoutSave, coreprotocol.HistoryRestoreEvent,
		coreprotocol.GitStagePaths, coreprotocol.GitUnstagePaths:
		return true
	}
	return false
}

// progressSink streams request-scoped partial output to the requesting shell
// only. Tokens accumulate; throttled publications carry the running text, so
// none is lost. The response carries the authoritative result.
func (a *actor) progressSink(request coreprotocol.Request) func(string) {
	var text strings.Builder
	var last time.Time
	return func(token string) {
		text.WriteString(token)
		if time.Since(last) < 50*time.Millisecond {
			return
		}
		last = time.Now()
		if a.progress != nil {
			a.progress(request.Owner, coreprotocol.Progress{RequestID: request.ID, Text: text.String()})
		}
	}
}
