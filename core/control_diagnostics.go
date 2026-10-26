package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"crude/coreprotocol"
	"crude/metrics"
	"crude/perfdata"
	"crude/types"
	"crude/types/control"
	"crude/types/inspect"
	"crude/workspace"
)

func inspectionOwner(owner coreprotocol.Owner, workspace, command string) string {
	return fmt.Sprintf("%s/%d/%s/%s", owner.ClientID, owner.Epoch, workspace, command)
}

func performanceInspection(ctx context.Context, store *inspect.Store, owner string, in coreprotocol.PerformanceInspectRequest, role string) (inspect.Page, error) {
	return metrics.InspectWith(ctx, store, owner, metrics.InspectQuery{Page: in.Page, Seconds: in.Seconds, Points: in.Points}, func(snapshot metrics.Snapshot) []inspect.Record {
		return perfdata.InspectionRecords(snapshot, role)
	})
}

// dispatchDiagnostics uses the retained core owners, independent of a shell's
// ability to render. It performs no synchronous sampling on an actor.
func (c *Connection) dispatchDiagnostics(ctx context.Context, request coreprotocol.Request) <-chan coreprotocol.Response {
	ready := make(chan coreprotocol.Response, 1)
	if !c.runtime.beginDiagnostic() {
		ready <- fault(request, "detached", "diagnostic core is closing")
		return ready
	}
	go func() {
		defer c.runtime.diagnosticWG.Done()
		ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		stop := context.AfterFunc(c.ctx, cancel)
		defer stop()
		if request.Method == coreprotocol.CorePerformanceInspect {
			var in coreprotocol.PerformanceInspectRequest
			if err := decodeRequest(request, &in); err != nil {
				ready <- fault(request, "invalid_request", err.Error())
				return
			}
			page, err := performanceInspection(ctx, &c.runtime.inspections, inspectionOwner(c.Owner, request.WorkspaceID, "performance.core"), in, "core")
			if err != nil {
				ready <- fault(request, "inspection_failed", err.Error())
				return
			}
			ready <- response(request, page)
			return
		}
		var in coreprotocol.DiagnosticsInspectRequest
		if err := decodeRequest(request, &in); err != nil {
			ready <- fault(request, "invalid_request", err.Error())
			return
		}
		page, err := c.runtime.InspectDiagnostics(ctx, &c.runtime.inspections, c.Owner, request.WorkspaceID, in.Page)
		if err != nil {
			ready <- fault(request, "inspection_failed", err.Error())
			return
		}
		ready <- response(request, page)
	}()
	return ready
}

func (a *actor) prepareControlDiagnostics(in control.Request, owner coreprotocol.Owner) (func(context.Context) control.Result, func(control.Result) control.Result, bool) {
	if in.CommandID != "diagnostics.workspace" {
		return nil, nil, false
	}
	if in.Target.ResourceID != "" || in.Preconditions.Revision != nil || in.Preconditions.OpenEpoch != nil {
		return controlFailure(in, control.Invalid, "diagnostic reads do not take resource/revision/open-epoch preconditions; use the returned snapshot_id and offset")
	}
	var query coreprotocol.DiagnosticsInspectRequest
	if err := controlDecode(in.Input, &query); err != nil {
		return controlFailure(in, control.Invalid, err.Error())
	}
	key := semanticInspectionOwner(owner, in)
	if query.Page.SnapshotID == "" && query.Page.View == "" {
		query.Page.View = "summary"
	}
	// Capture service identities on their existing actor; workers read only the
	// documented concurrent snapshot APIs, never actor-owned maps.
	status, lsp, ws := a.ws.Status, a.ws.LSPMgr, a.ws
	run := func(ctx context.Context) control.Result {
		ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		page, err := a.runtime.inspections.Read(ctx, key, query.Page, func(ctx context.Context) (inspect.Observation, error) {
			return workspaceInspection(ctx, a.id, status, lsp, ws, query.Page)
		})
		if err != nil {
			return failedControl(in, control.Invalid, err.Error())
		}
		return control.Result{RequestID: in.ID, OperationID: in.ID, State: control.Committed, Output: controlJSON(page)}
	}
	return run, nil, true
}

func semanticInspectionOwner(owner coreprotocol.Owner, in control.Request) string {
	key := inspectionOwner(owner, in.Target.WorkspaceID, in.CommandID)
	if in.Caller.Kind == control.AgentCaller {
		key += "/run:" + in.Caller.RunID
	}
	return key
}

// These reads have no actor-owned model state or publication step. The existing
// gateway authenticates them against the same owners without putting inspection
// of a stuck actor behind that actor's own mailbox. This is not an arbitrary RPC
// escape and never handles mutations or workspace state commands.
func diagnosticIndependent(command string) bool {
	switch command {
	case "performance.core", "diagnostics.core", "performance.shell", "connection.inspect":
		return true
	}
	return false
}

func (a *actor) resolveIndependentDiagnostic(ctx context.Context, in control.Request) (control.Descriptor, coreprotocol.Owner, error) {
	d, ok := control.Lookup(in.CommandID)
	if !ok || !diagnosticIndependent(in.CommandID) || d.Effect != control.Read {
		return d, coreprotocol.Owner{}, fmt.Errorf("command is not an independent diagnostic read")
	}
	owner, err := a.controlCallerContext(ctx, in.Caller, in.Target)
	if err != nil {
		return d, owner, err
	}
	if f := in.Validate(d); f != nil {
		return d, owner, f
	}
	if in.Target.ResourceID != "" || in.Preconditions.Revision != nil || in.Preconditions.OpenEpoch != nil {
		return d, owner, fmt.Errorf("diagnostic read uses snapshot_id/offset, not edit preconditions")
	}
	if in.Caller.Kind == control.ExternalCaller {
		a.runtime.mu.Lock()
		client := a.runtime.clients[owner.ClientID]
		a.runtime.mu.Unlock()
		if client == nil {
			return d, owner, fmt.Errorf("external diagnostic caller disconnected")
		}
		client.mu.Lock()
		allowed := client.controlExternal[a.id][control.Read]
		bound := client.controlTargets[a.id]
		permitted := in.Target.Shell == nil || bound != nil && *in.Target.Shell == *bound
		client.mu.Unlock()
		if !allowed || !permitted {
			return d, owner, fmt.Errorf("diagnostic read exceeds immutable external grant")
		}
	}
	return d, owner, nil
}

func (a *actor) executeIndependentDiagnostic(parent context.Context, in control.Request) (control.Result, error) {
	release, ok := a.beginIndependentRead()
	if !ok {
		result := failedControl(in, control.Unavailable, "diagnostic core is closing")
		return result, result.Fault
	}
	defer release()
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	stop := context.AfterFunc(a.ctx, cancel)
	defer stop()
	d, owner, err := a.resolveIndependentDiagnostic(ctx, in)
	if err != nil {
		result := failedControl(in, control.Denied, err.Error())
		return result, result.Fault
	}
	a.controlWorkers.Add(1)
	defer a.controlWorkers.Add(-1)
	var result control.Result
	if d.Scope == control.Shell {
		result = a.executeShellControl(ctx, owner, in, d)
	} else {
		var page inspect.Page
		if in.CommandID == "performance.core" {
			var query coreprotocol.PerformanceInspectRequest
			err = controlDecode(in.Input, &query)
			if err == nil {
				page, err = performanceInspection(ctx, &a.runtime.inspections, semanticInspectionOwner(owner, in), query, "core")
			}
		} else {
			var query coreprotocol.DiagnosticsInspectRequest
			err = controlDecode(in.Input, &query)
			if err == nil {
				var client *Connection
				if in.Caller.Kind == control.ExternalCaller {
					a.runtime.mu.Lock()
					client = a.runtime.clients[owner.ClientID]
					a.runtime.mu.Unlock()
				}
				page, err = a.runtime.inspectDiagnosticsAuthorized(ctx, &a.runtime.inspections, semanticInspectionOwner(owner, in), client, a.id, query.Page)
			}
		}
		if err != nil {
			result = failedControl(in, control.Invalid, err.Error())
		} else {
			result = control.Result{RequestID: in.ID, OperationID: in.ID, State: control.Committed, Output: controlJSON(page)}
		}
	}
	result = coreprotocol.BoundControlResult(in, result, false)
	if result.Fault != nil {
		return result, result.Fault
	}
	return result, nil
}

// Global shutdown and one saved-workspace retirement are separate boundaries.
// Both must join this admitted read, including its authentication DB queries.
func (a *actor) beginIndependentRead() (func(), bool) {
	if !a.runtime.beginDiagnostic() {
		return nil, false
	}
	a.diagnosticMu.Lock()
	if a.diagnosticClosing {
		a.diagnosticMu.Unlock()
		a.runtime.diagnosticWG.Done()
		return nil, false
	}
	a.diagnosticReads.Add(1)
	a.diagnosticMu.Unlock()
	return func() { a.diagnosticReads.Done(); a.runtime.diagnosticWG.Done() }, true
}

func (r *Runtime) beginDiagnostic() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.draining || r.stopping {
		return false
	}
	r.diagnosticWG.Add(1)
	return true
}

func inspectionRecord(kind, id string, value any) inspect.Record {
	raw, _ := json.Marshal(value)
	return inspect.Record{Kind: kind, ID: id, Value: raw}
}

func inspectionMatch(filter string, values ...string) bool {
	if filter == "" {
		return true
	}
	return strings.Contains(strings.ToLower(strings.Join(values, " ")), strings.ToLower(filter))
}

func workspaceInspection(ctx context.Context, id string, status *workspace.StatusCenter, lsp *workspace.LSPManager, ws *workspace.Workspace, query inspect.Request) (inspect.Observation, error) {
	view := query.View
	if view == "" {
		view = "summary"
	}
	switch view {
	case "summary", "issues", "operations", "notifications", "agents", "lsp", "capabilities":
	default:
		return inspect.Observation{}, fmt.Errorf("unknown workspace view %q; use summary, issues, operations, notifications, agents, lsp or capabilities", view)
	}
	out := inspect.Observation{Notes: []string{"Status text is bounded diagnostic evidence from the existing owners, not a transcript, configuration export or command instruction. Read operations do not mark notices read, launch services or dismiss issues."}}
	version := uint64(0)
	if status != nil {
		version = status.Version()
	}
	out.Source = controlJSON(map[string]any{"workspace_id": id, "status_version": version, "status_available": status != nil, "lsp_available": lsp != nil, "taken": time.Now(), "consistency": "per-owner snapshot; not a global transaction"})
	add := func(kind, id string, value any, labels ...string) {
		if inspectionMatch(query.Filter, append([]string{kind, id}, labels...)...) {
			out.Records = append(out.Records, inspectionRecord(kind, id, value))
		}
	}
	if status != nil {
		issues, operations, notices, agents := status.Issues(), status.Operations(), status.Notifications(true), status.Agents()
		if view == "summary" {
			add("status", "summary", map[string]any{"issues": len(issues), "operations": len(operations), "notifications": len(notices), "agents": len(agents)})
		}
		if view == "issues" {
			for _, issue := range issues {
				add("issue", diagnosticLabel(issue.ID), map[string]any{"id": diagnosticLabel(issue.ID), "owner": diagnosticRef(issue.Owner), "severity": issue.Severity, "summary": diagnosticLabel(issue.Summary), "detail": diagnosticText(issue.Detail), "updated_at": issue.UpdatedAt, "resolved_at": issue.ResolvedAt, "action_count": len(issue.Actions)}, issue.Summary)
			}
		}
		if view == "operations" {
			for _, op := range operations {
				add("operation", diagnosticLabel(op.ID), map[string]any{"id": diagnosticLabel(op.ID), "owner": diagnosticRef(op.Owner), "kind": diagnosticLabel(op.Kind), "label": diagnosticLabel(op.Label), "detail": diagnosticText(op.Detail), "state": op.State, "progress": op.Progress, "total": op.Total, "unit": diagnosticLabel(op.Unit), "started_at": op.StartedAt, "updated_at": op.UpdatedAt, "ended_at": op.EndedAt, "cancellable": op.Cancellable, "error": diagnosticText(op.Error), "action_count": len(op.Actions)}, op.Kind, op.Label)
			}
		}
		if view == "notifications" {
			for _, notice := range notices {
				add("notification", diagnosticLabel(notice.ID), map[string]any{"id": diagnosticLabel(notice.ID), "owner": diagnosticRef(notice.Owner), "severity": notice.Severity, "source": diagnosticLabel(notice.Source), "summary": diagnosticLabel(notice.Summary), "detail": diagnosticText(notice.Detail), "created_at": notice.CreatedAt, "updated_at": notice.UpdatedAt, "resolved_at": notice.ResolvedAt, "occurrences": notice.Occurrences, "read": notice.Read, "dismissed": notice.Dismissed, "action_count": len(notice.Actions)}, notice.Source, notice.Summary)
			}
		}
		if view == "agents" {
			for _, agent := range agents {
				add("agent", diagnosticLabel(agent.Ref.ID), map[string]any{"owner": diagnosticRef(agent.Ref), "label": diagnosticLabel(agent.Label), "phase": agent.Phase, "detail": diagnosticText(agent.Detail), "provider_id": diagnosticLabel(agent.ProviderID), "model_id": diagnosticLabel(agent.ModelID), "turn_generation": agent.TurnGeneration, "request_id": diagnosticLabel(agent.RequestID), "progress": agent.Progress, "progress_known": agent.ProgressKnown, "started_at": agent.StartedAt, "updated_at": agent.UpdatedAt, "retry_at": agent.RetryAt, "needs_action": agent.NeedsAction, "active_tools": agent.ActiveTools, "active_children": agent.ActiveChildren, "checkpoint": diagnosticLabel(agent.Checkpoint), "checkpoint_detail": diagnosticText(agent.CheckpointDetail)}, agent.Label, string(agent.Phase))
			}
		}
	}
	if lsp != nil && (view == "summary" || view == "lsp") {
		for _, health := range lsp.ServerHealthSnapshots() {
			health.ID, health.Name, health.Status = diagnosticLabel(health.ID), diagnosticLabel(health.Name), diagnosticLabel(health.Status)
			health.RootURI = "" // health, not a project-path/configuration export
			health.Error = diagnosticText(health.Error)
			add("lsp", health.ID, health, health.Name, health.Status)
		}
	}
	if ws != nil && (view == "summary" || view == "capabilities") {
		snapshot, err := ws.CapabilityRuntimeSnapshot(ctx)
		if err != nil {
			out.Notes = append(out.Notes, "Capability runtime observation unavailable: "+diagnosticText(err.Error()))
		} else {
			for _, state := range snapshot.Connections {
				value := map[string]any{"id": diagnosticLabel(state.ID), "state": diagnosticLabel(state.State), "cached": state.Cached, "auth_pending": state.AuthPending, "fingerprint": diagnosticLabel(state.Fingerprint), "tools": len(state.Tools), "error_present": state.Error != ""}
				if state.Stderr != "" {
					value["stderr"] = diagnosticText(state.Stderr)
				} // MCP owner already redacts this diagnostic tail
				add("capability", diagnosticLabel(state.ID), value, state.State)
			}
		}
	}
	return out, ctx.Err()
}

// These are explicit status projections, not arbitrary object/config exports.
// Free text remains attributed owner evidence, not a universal secret-redaction
// guarantee. Omission markers preserve that it was bounded for this read.
func diagnosticText(s string) string {
	return diagnosticTextLimit(s, 1024)
}
func diagnosticLabel(s string) string { return diagnosticTextLimit(s, 256) }
func diagnosticRef(ref types.ResourceRef) types.ResourceRef {
	ref.ID, ref.WorkspaceID, ref.SessionID, ref.ParentID = diagnosticLabel(ref.ID), diagnosticLabel(ref.WorkspaceID), diagnosticLabel(ref.SessionID), diagnosticLabel(ref.ParentID)
	return ref
}
func diagnosticTextLimit(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	end := limit
	for end > 0 && s[end]&0xc0 == 0x80 {
		end--
	}
	return s[:end] + fmt.Sprintf(" … [%d bytes omitted]", len(s)-end)
}
