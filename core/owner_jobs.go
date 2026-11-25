package core

import (
	"context"
	"errors"
	"strings"
	"time"

	"crude/core/coreprotocol"
	"crude/workspace"
	"crude/workspace/document"
)

// prepareOwner serves owner operations whose exact target is resolved on the
// actor (where the local shell resolved it) but whose I/O runs on a worker.
func (a *actor) prepareOwner(request coreprotocol.Request) (func(context.Context) coreprotocol.Response, bool) {
	if run, handled := a.prepareCoreRestart(request); handled {
		return run, true
	}
	ws := a.ws
	bad := func(err error) (func(context.Context) coreprotocol.Response, bool) {
		return func(context.Context) coreprotocol.Response { return fault(request, "invalid_request", err.Error()) }, true
	}
	done := func(value coreprotocol.Value) (func(context.Context) coreprotocol.Response, bool) {
		return func(context.Context) coreprotocol.Response { return responseValue(request, value) }, true
	}
	switch request.Method {
	case coreprotocol.WorkspaceOpenFolder:
		if err := decodeRequest(request, &coreprotocol.Empty{}); err != nil {
			return bad(err)
		}
		a.openProjectFolder()
		docs := ws.LiveDocuments
		owner := document.Owner{ClientID: request.Owner.ClientID, Epoch: request.Owner.Epoch}
		return func(context.Context) coreprotocol.Response {
			docs.OpenFolder(owner)
			return response(request, coreprotocol.Empty{})
		}, true
	case coreprotocol.AgentRecover, coreprotocol.AgentRecoverCancel:
		var in coreprotocol.RecoveryRequest
		if err := decodeRequest(request, &in); err != nil {
			return bad(err)
		}
		run := ws.PrepareAgentRecovery(in.AgentID, in.IncidentID, in.Generation)
		if request.Method == coreprotocol.AgentRecoverCancel {
			run = ws.PrepareCancelAgentRecovery(in.AgentID, in.IncidentID, in.Generation)
		}
		if run == nil {
			return done(coreprotocol.BoolResult{Error: "no session owns this agent"})
		}
		return func(context.Context) coreprotocol.Response {
			if err := run(); err != nil {
				return response(request, coreprotocol.BoolResult{Error: err.Error()})
			}
			return response(request, coreprotocol.BoolResult{OK: true})
		}, true
	case coreprotocol.AgentPlanMode:
		var in coreprotocol.AgentPlanModeRequest
		if err := decodeRequest(request, &in); err != nil {
			return bad(err)
		}
		run, err := ws.PrepareSetPlanMode(in.AgentID, in.Enabled)
		if err != nil {
			return done(coreprotocol.BoolResult{Error: err.Error()})
		}
		return func(context.Context) coreprotocol.Response {
			if err := run(); err != nil {
				return response(request, coreprotocol.BoolResult{Error: err.Error()})
			}
			return response(request, coreprotocol.BoolResult{OK: true})
		}, true
	case coreprotocol.AgentPlanEvidence:
		var in coreprotocol.AgentPlanEvidenceRequest
		if err := decodeRequest(request, &in); err != nil {
			return bad(err)
		}
		run, err := ws.PrepareReadPlanEvidence(in.AgentID, in.Revision, in.Digest)
		if err != nil {
			return done(wire(coreprotocol.AgentPlanEvidenceResult{Error: err.Error()}))
		}
		return func(ctx context.Context) coreprotocol.Response {
			if err := ctx.Err(); err != nil {
				return fault(request, "cancelled", err.Error())
			}
			text, err := run()
			if err != nil {
				return response(request, coreprotocol.AgentPlanEvidenceResult{Error: err.Error()})
			}
			return response(request, coreprotocol.AgentPlanEvidenceResult{Text: text})
		}, true
	case coreprotocol.AgentPlanReviewOpen:
		var in coreprotocol.AgentPlanReviewOpenRequest
		if err := decodeRequest(request, &in); err != nil {
			return bad(err)
		}
		run, err := ws.PrepareOpenPlanRevision(in.AgentID, in.Revision)
		if err != nil {
			return done(wire(coreprotocol.AgentPlanReviewOpenResult{Error: err.Error()}))
		}
		return func(context.Context) coreprotocol.Response {
			path, err := run()
			if err != nil {
				return response(request, coreprotocol.AgentPlanReviewOpenResult{Error: err.Error()})
			}
			return response(request, coreprotocol.AgentPlanReviewOpenResult{Path: path})
		}, true
	case coreprotocol.AgentPlanComment:
		var in coreprotocol.AgentPlanCommentRequest
		if err := decodeRequest(request, &in); err != nil {
			return bad(err)
		}
		run, err := ws.PrepareEditPlanComment(in.AgentID, in.Edit)
		if err != nil {
			return done(coreprotocol.BoolResult{Error: err.Error()})
		}
		return func(context.Context) coreprotocol.Response {
			if err := run(); err != nil {
				return response(request, coreprotocol.BoolResult{Error: err.Error()})
			}
			return response(request, coreprotocol.BoolResult{OK: true})
		}, true
	case coreprotocol.AgentPlanSettle:
		var in coreprotocol.AgentPlanSettleRequest
		if err := decodeRequest(request, &in); err != nil {
			return bad(err)
		}
		input := humanInput(request, coreprotocol.InputRequest{AgentID: in.AgentID})
		a.bindInputExecution(&input, in.AgentID)
		run, err := ws.PrepareSettlePlanReview(in.AgentID, in.Settle, input)
		if err != nil {
			return done(wire(coreprotocol.AgentPlanSettleResult{Error: err.Error()}))
		}
		return func(ctx context.Context) coreprotocol.Response {
			settlement, err := run(ctx)
			if err != nil {
				return response(request, coreprotocol.AgentPlanSettleResult{Error: err.Error()})
			}
			return response(request, coreprotocol.AgentPlanSettleResult{Settlement: settlement})
		}, true
	case coreprotocol.AgentGoal:
		var in coreprotocol.AgentGoalRequest
		if err := decodeRequest(request, &in); err != nil {
			return bad(err)
		}
		run, err := ws.PrepareGoalAction(in.AgentID, in.Action, in.Objective)
		if err != nil {
			return done(wire(coreprotocol.AgentGoalResult{Error: err.Error()}))
		}
		return func(context.Context) coreprotocol.Response {
			goal, present, err := run()
			if err != nil {
				return response(request, coreprotocol.AgentGoalResult{Error: err.Error()})
			}
			out := coreprotocol.AgentGoalResult{}
			if present {
				out.Goal = &goal
			}
			return response(request, out)
		}, true
	case coreprotocol.AgentLoop:
		var in coreprotocol.AgentLoopRequest
		if err := decodeRequest(request, &in); err != nil {
			return bad(err)
		}
		run, err := ws.PrepareLoopAction(in.AgentID, in.Action, in.ID, in.Prompt, time.Duration(in.Interval))
		if err != nil {
			return done(coreprotocol.BoolResult{Error: err.Error()})
		}
		return func(context.Context) coreprotocol.Response {
			if err := run(); err != nil {
				return response(request, coreprotocol.BoolResult{Error: err.Error()})
			}
			return response(request, coreprotocol.BoolResult{OK: true})
		}, true
	case coreprotocol.AgentStopCommand:
		var in coreprotocol.StopCommandRequest
		if err := decodeRequest(request, &in); err != nil {
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
		if err := decodeRequest(request, &in); err != nil {
			return bad(err)
		}
		// The owning session alone decides admission: it compacts now, after
		// a finishing turn, or at the active turn's next step.
		prepared, result := ws.PrepareCompactForAgent(in.AgentID)
		if prepared.Run == nil {
			return done(wire(result))
		}
		// The boundary row is reduced before the job starts, so every shell
		// shows the admitted operation at once.
		if reduced := ws.ApplyAgentEvent(workspace.AgentEvent{Type: "context_compaction_started", AgentID: in.AgentID, Data: prepared.Started}); reduced.Accepted {
			a.reduced(reduced)
		}
		return func(ctx context.Context) coreprotocol.Response {
			// Actor shutdown and exact request cancellation own the admitted job;
			// joining workers must not wait forever for a provider extraction.
			stop := context.AfterFunc(ctx, prepared.Cancel)
			defer stop()
			return response(request, prepared.Run())
		}, true
	case coreprotocol.CommandViewOpen:
		var in coreprotocol.CommandViewOpenRequest
		if err := decodeRequest(request, &in); err != nil {
			return bad(err)
		}
		owner := workspace.TerminalController{ClientID: request.Owner.ClientID, Epoch: request.Owner.Epoch}
		open, err := ws.PrepareOpenCommandView(in.AgentID, in.Ref, owner, in.Cols, in.Rows)
		if err != nil {
			return bad(err)
		}
		return func(ctx context.Context) coreprotocol.Response {
			value, err := open(ctx)
			if err != nil {
				return fault(request, "unavailable", err.Error())
			}
			if err := ctx.Err(); err != nil {
				ws.Terminals.Close(value.ID)
				return fault(request, "cancelled", err.Error())
			}
			return response(request, value)
		}, true
	case coreprotocol.HistoryUndoScopes:
		var in coreprotocol.UndoScopesRequest
		if err := decodeRequest(request, &in); err != nil {
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
		if err := decodeRequest(request, &in); err != nil {
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
		if err := decodeRequest(request, &in); err != nil {
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
				if errors.Is(err, workspace.ErrGitNoChanges) {
					return fault(request, "git_no_changes", err.Error())
				}
				return fault(request, "generation_failed", err.Error())
			}
			return response(request, coreprotocol.CommitMessageResult{Message: message})
		}, true
	}
	return nil, false
}

// ownerMutation reports which owner operations are journaled.
func ownerMutation(method string) bool {
	if coreRestartMethod(method) {
		return method != coreprotocol.CoreRestartInspect
	}
	switch method {
	case coreprotocol.WorkspaceOpenFolder, coreprotocol.AgentRecover, coreprotocol.AgentRecoverCancel, coreprotocol.AgentStopCommand, coreprotocol.AgentCompact, coreprotocol.AgentPlanMode, coreprotocol.AgentGoal, coreprotocol.AgentLoop,
		coreprotocol.AgentPlanReviewOpen, coreprotocol.AgentPlanComment, coreprotocol.AgentPlanSettle,
		coreprotocol.WorkspaceOverridesSet,
		coreprotocol.GitStagePaths, coreprotocol.GitUnstagePaths:
		return true
	}
	return false
}

func ownerOperation(method string) bool {
	if coreRestartMethod(method) {
		return true
	}
	switch method {
	case coreprotocol.WorkspaceOpenFolder, coreprotocol.AgentRecover, coreprotocol.AgentRecoverCancel, coreprotocol.AgentStopCommand, coreprotocol.AgentCompact, coreprotocol.AgentPlanMode, coreprotocol.AgentGoal, coreprotocol.AgentLoop,
		coreprotocol.AgentPlanReviewOpen, coreprotocol.AgentPlanComment, coreprotocol.AgentPlanSettle,
		coreprotocol.HistoryUndoScopes, coreprotocol.CommandViewOpen, coreprotocol.AgentPlanEvidence,
		coreprotocol.GitStagePaths, coreprotocol.GitUnstagePaths, coreprotocol.GitCommitModel, coreprotocol.GitCommitMessage:
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
