package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"crude/collaboration"
	"crude/coreprotocol"
	"crude/document"
	"crude/provider"
	"crude/session"
	"crude/types/control"
	"crude/workspace"
	"crude/worktree"
)

// Exercise framed external allocation/selection/input admission and the actual
// actor gateway for a live parent's two real StartAgent results. The provider
// barrier keeps the parent's exact durable run current without fake inventory.
func TestControlExecutionConnectedSelectionAndChildIntegration(t *testing.T) {
	fixture := controlExecutionFixture(t)
	ws := fixture.ws
	root, home := ws.RootPath, t.TempDir()
	var calls atomic.Int32
	var mu sync.Mutex
	steps := map[string]int{}
	rootReady, rootRelease := make(chan struct{}), make(chan struct{})
	var release sync.Once
	defer release.Do(func() { close(rootRelease) })
	var parentID, selectedRoot string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body struct {
			Messages []provider.ChatMessage `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		raw, _ := json.Marshal(body)
		key := "root"
		if strings.Contains(string(raw), "CHILD_FIRST") {
			key = "first"
		}
		if strings.Contains(string(raw), "CHILD_SECOND") {
			key = "second"
		}
		mu.Lock()
		step := steps[key]
		steps[key]++
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		name := ""
		params := map[string]any{}
		if key == "root" {
			if step == 0 {
				name, params = "read_file", map[string]any{"path": "disk.txt"}
			} else {
				if !strings.Contains(string(raw), wireText(selectedRoot)) || !strings.Contains(string(raw), "human dirty") {
					t.Error("root request did not use selected checkout and dirty baseline")
				}
				close(rootReady)
				select {
				case <-rootRelease:
				case <-r.Context().Done():
					return
				}
			}
		} else {
			switch step {
			case 0:
				name, params = "read_file", map[string]any{"path": "live.txt"}
			case 1:
				name, params = "edit_file", map[string]any{"path": "live.txt", "old_string": "two", "new_string": "TWO"}
			case 2:
				name, params = "publish_report", map[string]any{"kind": "final", "recipient_ids": []string{parentID}, "summary": key + " isolated", "body": "Completed isolated edit."}
			}
		}
		if name == "" {
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Finished.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			return
		}
		args, _ := json.Marshal(params)
		_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":%q,\"type\":\"function\",\"function\":{\"name\":%q,\"arguments\":%q}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n", fmt.Sprintf("%s-%d", key, step), name, string(args))
	}))
	defer server.Close()
	agents := session.DefaultAgentsConfig()
	profile := agents.Profiles["default"]
	on := true
	profile.MultiAgent = &on
	for _, category := range []string{"default", "crafter"} {
		profile.Categories[category] = session.CategoryConfig{AccountID: "test", Model: "test-model", ModelSupportsTools: true}
	}
	agents.Profiles["default"] = profile
	cfg := &session.Config{Agents: agents, Accounts: map[string]provider.Account{"test": {ID: "test", ProviderID: "openai", APIKey: "test", Connection: provider.Template{Name: "test", ProviderURL: server.URL, APIStandard: "openai", AuthType: provider.AuthProtocol}}}}
	ws.SetBackendConfig(cfg, home)
	runtime, err := New(func(string) (*workspace.Workspace, error) { return ws, nil }, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { release.Do(func() { close(rootRelease) }); _ = runtime.Close() }()
	c := controlWireClient(t, runtime, "execution-connected")
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	var attached coreprotocol.AttachResult
	if err := c.Call(ctx, "", coreprotocol.Attach, coreprotocol.AttachRequest{Root: root}, &attached); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(ctx, attached.WorkspaceID, coreprotocol.ControlClientRegister, &coreprotocol.ControlClientGrant{Effects: []control.Effect{control.Read, control.Write, control.Execute, control.Delete}}, nil); err != nil {
		t.Fatal(err)
	}
	var created workspace.NewSessionResult
	if err := c.Call(ctx, attached.WorkspaceID, coreprotocol.NewSession, &coreprotocol.InvocationSessionRequest{}, &created); err != nil || created.Error != "" {
		t.Fatal(created, err)
	}
	parentID = created.OrchID
	backend := ws.BackendSession(created.SessionID)
	backend.SetYolo(true)
	next := 0
	wire := func(command, id string, input any) control.Result {
		t.Helper()
		next++
		raw, _ := json.Marshal(input)
		request := control.Request{ID: fmt.Sprintf("execution-wire-%d", next), CommandID: command, Target: control.Target{WorkspaceID: attached.WorkspaceID, SessionID: created.SessionID, ResourceID: id}, Input: raw}
		var result control.Result
		payload, _ := coreprotocol.Marshal(coreprotocol.Ptr(request))
		response, err := c.CallRequest(ctx, coreprotocol.Request{ID: request.ID, Owner: c.Owner(), WorkspaceID: attached.WorkspaceID, Method: coreprotocol.ControlExecute, Payload: payload})
		if err != nil || response.Error != nil {
			t.Fatal(err, response.Error)
		}
		if err := coreprotocol.Decode(response.Payload, &result); err != nil {
			t.Fatal(err)
		}
		if result.Fault != nil {
			t.Fatal(command, result.Fault)
		}
		return result
	}
	allocated := wire("execution.create", "", map[string]any{"mode": "worktree"})
	var execution worktree.Context
	if err := json.Unmarshal(allocated.Output, &execution); err != nil {
		t.Fatal(err)
	}
	selectedRoot = execution.Root
	if len(execution.Pins) != 0 {
		t.Fatal("external allocation pinned nonexistent task")
	}
	wire("execution.select", execution.ID, map[string]any{"id": execution.ID, "expected_revision": 0})
	if backend.ExecutionSelection().ID != execution.ID {
		t.Fatal("selection not persisted in real backend")
	}
	if err := c.Call(ctx, attached.WorkspaceID, coreprotocol.SubmitInput, &coreprotocol.InputRequest{AgentID: parentID, Text: "Read dirty baseline in selected execution.", DraftRevision: 1}, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-rootReady:
	case <-ctx.Done():
		t.Fatal("root provider did not reach selected execution", ctx.Err())
	}
	human := document.Owner{ClientID: "human", Epoch: 1}
	snap, err := ws.LiveDocuments.Open(human, "live.txt")
	if err != nil {
		t.Fatal(err)
	}
	edited, err := ws.LiveDocuments.Apply(document.Change{DocumentID: snap.ID, RequestID: "accepted-before-children", Owner: human, BaseRevision: snap.Revision, GroupID: "human", Edits: []document.Edit{{Start: 0, End: 3, Text: "ONE"}}})
	if err != nil {
		t.Fatal(err)
	}
	runner := backend.SubAgentRunner()
	if runner == nil {
		runner = session.NewSubAgentRunner(backend, 2)
		runner.SetOrchestratorID(parentID)
		runner.SetEffectiveConfig(cfg)
	}
	for _, marker := range []string{"CHILD_FIRST", "CHILD_SECOND"} {
		result, err := runner.StartAgent(ctx, map[string]any{"category": "crafter", "description": marker, "instructions": "Read live.txt, edit two to TWO, publish the final report.", "file_ownership": []string{"live.txt"}, "isolation": "worktree"})
		if err != nil {
			t.Fatal(result.Content, err)
		}
	}
	done := make(chan struct{})
	go func() { runner.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		runner.CancelAllActive()
		t.Fatal("children did not settle", ctx.Err())
	}
	children := []worktree.Context{}
	for _, artifact := range ws.Executions.List() {
		if artifact.Owner.AgentID != execution.Owner.AgentID {
			children = append(children, artifact)
		}
	}
	if len(children) != 2 || children[0].Root == children[1].Root {
		t.Fatal("two actual isolated children missing", children)
	}
	// Even an ancestor with an immutable effect approval cannot apply an active
	// descendant. The exact durable owner run, not presence alone, must settle.
	active, err := ws.Collaboration.RegisterAgent(ctx, collaboration.AgentRegistration{ID: "active-descendant", Name: "Active", SessionID: "active-session", ParentID: parentID, OwnerInstance: "active-owner"})
	if err != nil {
		t.Fatal(err)
	}
	activeRun, err := ws.Collaboration.StartRun(ctx, collaboration.StartRunRequest{ID: "active-descendant-run", AgentID: active.ID, RuntimeEpoch: active.RuntimeEpoch, Origin: collaboration.OriginRuntime})
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.controlExecutionOwner(ctx, control.Request{CommandID: "execution.apply", Caller: control.Caller{Kind: control.AgentCaller, AgentID: parentID}}, worktree.Owner{AgentID: active.ID, RunID: activeRun.ID}); err == nil {
		t.Fatal("active descendant accepted for integration")
	}
	parent, err := ws.Collaboration.Agent(ctx, parentID)
	if err != nil {
		t.Fatal(err)
	}
	runtime.mu.Lock()
	a := runtime.workspaces[attached.WorkspaceID]
	runtime.mu.Unlock()
	gateway := &actorControlRuntime{actor: a}
	parentCall := func(command, id string, input any) control.Result {
		t.Helper()
		next++
		raw, _ := json.Marshal(input)
		request := control.Request{ID: fmt.Sprintf("parent-gateway-%d", next), CommandID: command, Caller: control.Caller{Kind: control.AgentCaller, AgentID: parentID, RunID: parent.CurrentRunID}, Target: control.Target{WorkspaceID: attached.WorkspaceID, SessionID: created.SessionID, ResourceID: id}, Input: raw}
		d, err := gateway.Resolve(ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		approved := control.NewApproval(request, d).CapabilityApprovalContext(ctx)
		result, err := gateway.Execute(approved, request)
		if err != nil {
			t.Fatal(command, err, result)
		}
		return result
	}
	_, err = ws.LiveDocuments.Apply(document.Change{DocumentID: snap.ID, RequestID: "accepted-after-children", Owner: human, BaseRevision: edited.Revision, GroupID: "later", Edits: []document.Edit{{Start: 8, End: 13, Text: "THREE"}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, child := range children {
		run, err := ws.Collaboration.Run(ctx, child.Owner.RunID)
		if err != nil || !run.State.Terminal() || len(child.Pins) != 0 {
			t.Fatal("child not settled", run, err, child.Pins)
		}
		var plan worktree.PlanDelta
		if err := json.Unmarshal(parentCall("execution.review", child.ID, map[string]any{"id": child.ID}).Output, &plan); err != nil {
			t.Fatal(err)
		}
		var receipt worktree.Receipt
		if err := json.Unmarshal(parentCall("execution.apply", child.ID, map[string]any{"plan": plan}).Output, &receipt); err != nil {
			t.Fatal(err)
		}
		accepted, err := ws.LiveDocuments.Snapshot(snap.ID)
		if err != nil || string(accepted.Text) != "ONE\nTWO\nTHREE\n" || !accepted.Dirty {
			t.Fatal("lost accepted human text", accepted, err)
		}
		parentCall("execution.undo", receipt.OperationID, map[string]any{"operation_id": receipt.OperationID})
		accepted, err = ws.LiveDocuments.Snapshot(snap.ID)
		if err != nil || string(accepted.Text) != "ONE\ntwo\nTHREE\n" {
			t.Fatal("undo lost newer human text", accepted, err)
		}
	}
	if data, err := os.ReadFile(filepath.Join(root, "disk.txt")); err != nil || string(data) != "human dirty\n" {
		t.Fatal("dirty baseline changed", string(data), err)
	}
	if data, err := os.ReadFile(filepath.Join(root, "live.txt")); err != nil || string(data) != "one\ntwo\nthree\n" {
		t.Fatal("accepted text forced to disk", string(data), err)
	}
	// Clearing the next-input selection must not move the currently pinned root.
	wire("execution.select", "", map[string]any{"id": "", "expected_revision": 1})
	artifact, err := ws.Executions.Inspect(execution.ID)
	if err != nil || len(artifact.Pins) != 1 || artifact.Pins[0].AgentID != parentID {
		t.Fatal("live root moved with selection", artifact, err)
	}
	release.Do(func() { close(rootRelease) })
	// Workspace peers may review settled work for janitorial cleanup, even
	// though they did not launch that child. The gateway still fences callers.
	other, err := ws.Collaboration.RegisterAgent(ctx, collaboration.AgentRegistration{ID: "foreign-root", Name: "Foreign", SessionID: "foreign-session", OwnerInstance: "foreign-owner"})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.controlExecutionOwner(ctx, control.Request{CommandID: "execution.review", Caller: control.Caller{Kind: control.AgentCaller, AgentID: other.ID, RunID: "foreign-run"}}, children[0].Owner); err != nil {
		t.Fatal("workspace janitor could not review settled work", err)
	}
}
