package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"crude/collaboration"
	"crude/coreclient"
	"crude/coreprotocol"
	"crude/internal/wiretest"
	"crude/types"
	"crude/types/control"
	"crude/types/inspect"
	"crude/workspace"
)

func TestDiagnosticTypedUnattachedRPCAndOwnerBounds(t *testing.T) {
	r := transportRuntime(t)
	c := controlWireClient(t, r, "diagnostic-unattached")
	peer := controlWireClient(t, r, "diagnostic-peer")
	var performance inspect.Page
	if err := c.Call(t.Context(), "", coreprotocol.CorePerformanceInspect, &coreprotocol.PerformanceInspectRequest{}, &performance); err != nil {
		t.Fatal("unattached performance RPC", err)
	}
	var source struct {
		PID         int       `json:"pid"`
		Incarnation string    `json:"incarnation"`
		Taken       time.Time `json:"taken"`
	}
	if err := json.Unmarshal(performance.Source, &source); err != nil || source.PID != os.Getpid() || source.Incarnation == "" || source.Taken.IsZero() {
		t.Fatalf("actual process provenance missing: %s (%v)", performance.Source, err)
	}
	var page inspect.Page
	if err := c.Call(t.Context(), "", coreprotocol.CoreDiagnosticsInspect, &coreprotocol.DiagnosticsInspectRequest{Page: inspect.Request{View: "clients", Limit: 1, MaxBytes: 4096}}, &page); err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 || page.Next == nil || page.Returned != 1 || page.Complete {
		t.Fatalf("dense client continuation missing: %+v", page)
	}
	raw, err := json.Marshal(page)
	if err != nil || !json.Valid(raw) || len(raw) > 4096 || !strings.Contains(string(raw), "diagnostic-") {
		t.Fatalf("unreadable or oversized RPC evidence: %s (%v)", raw, err)
	}
	var next inspect.Page
	if err := c.Call(t.Context(), "", coreprotocol.CoreDiagnosticsInspect, &coreprotocol.DiagnosticsInspectRequest{Page: *page.Next}, &next); err != nil {
		t.Fatal(err)
	}
	if next.SnapshotID != page.SnapshotID || !reflect.DeepEqual(next.Source, page.Source) || next.Offset != 1 || !next.Complete || next.Records[0].ID == page.Records[0].ID {
		t.Fatal("continuation did not retain immutable actual observation", next)
	}
	for _, test := range []struct {
		name string
		page inspect.Request
	}{
		{"past_end", inspect.Request{SnapshotID: page.SnapshotID, Offset: page.Total + 1}},
		{"missing_snapshot", inspect.Request{SnapshotID: "not-a-retained-snapshot", Offset: 1}},
		{"negative_offset", inspect.Request{SnapshotID: page.SnapshotID, Offset: -1}},
		{"changed_view", inspect.Request{SnapshotID: page.SnapshotID, View: "requests"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := c.Call(t.Context(), "", coreprotocol.CoreDiagnosticsInspect, &coreprotocol.DiagnosticsInspectRequest{Page: test.page}, &next); err == nil {
				t.Fatal("malicious continuation admitted")
			}
		})
	}
	if err := peer.Call(t.Context(), "", coreprotocol.CoreDiagnosticsInspect, &coreprotocol.DiagnosticsInspectRequest{Page: *page.Next}, &next); err == nil {
		t.Fatal("foreign transport owner continued snapshot")
	}
	if err := c.Call(t.Context(), "foreign-workspace", coreprotocol.CoreDiagnosticsInspect, &coreprotocol.DiagnosticsInspectRequest{}, &next); err == nil {
		t.Fatal("unattached workspace diagnostics admitted")
	}
	r.mu.Lock()
	owner := r.clients[c.Owner().ClientID]
	r.mu.Unlock()
	payload, err := wiretest.Marshal(coreprotocol.DiagnosticsInspectRequest{})
	if err != nil {
		t.Fatal(err)
	}
	forged := owner.Owner
	forged.Epoch++
	out := owner.Call(t.Context(), coreprotocol.Request{ID: "forged-observer", Owner: forged, Method: coreprotocol.CoreDiagnosticsInspect, Payload: payload})
	if out.Error == nil {
		t.Fatal("forged epoch reached typed observer")
	}
	r.mu.Lock()
	workspaces := len(r.workspaces)
	r.mu.Unlock()
	if workspaces != 0 {
		t.Fatal("unattached diagnostic RPC opened a workspace")
	}
}

func TestDiagnosticExternalGatewaySurvivesBlockedCurrentActor(t *testing.T) {
	r, err := New(func(root string) (*workspace.Workspace, error) {
		return workspace.NewWorkspace(root, "", "tester", types.Settings{}, coreTestLogger{}), nil
	}, t.TempDir()+"/receipts.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	c := controlWireClient(t, r, "blocked-current-observer")
	var attached coreprotocol.AttachResult
	if err := c.Call(t.Context(), "", coreprotocol.Attach, &coreprotocol.AttachRequest{Root: t.TempDir()}, &attached); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(t.Context(), attached.WorkspaceID, coreprotocol.ControlClientRegister, &coreprotocol.ControlClientGrant{Effects: []control.Effect{control.Read}}, nil); err != nil {
		t.Fatal(err)
	}
	a := r.actors()[0]
	participant, err := a.ws.Collaboration.RegisterAgent(t.Context(), collaboration.AgentRegistration{ID: "blocked-native-root", Name: "Diagnostic root", SessionID: "blocked-root-session", OwnerInstance: "diagnostic-test"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := a.ws.Collaboration.StartRun(t.Context(), collaboration.StartRunRequest{AgentID: participant.ID, RuntimeEpoch: participant.RuntimeEpoch, Origin: collaboration.OriginHuman})
	if err != nil {
		t.Fatal(err)
	}
	entered, release, joined := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		joined <- a.inspect(context.Background(), func() { close(entered); <-release })
	}()
	<-entered
	defer func() { close(release); <-joined }()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	var out control.Result
	input := json.RawMessage(`{"page":{"view":"workspaces","max_bytes":4096}}`)
	if _, err := c.CallWith(ctx, attached.WorkspaceID, coreprotocol.ControlExecute, &control.Request{ID: "blocked-current-diagnostics", CommandID: "diagnostics.core", Input: input}, &out, coreclient.CallOptions{ID: "blocked-current-diagnostics"}); err != nil {
		t.Fatal("semantic diagnostic depended on the blocked actor it must diagnose", err)
	}
	if out.Fault != nil || out.State != control.Committed || !json.Valid(out.Output) || len(out.Output) > 4096 {
		t.Fatalf("blocked-current semantic diagnostic unavailable: %+v", out)
	}
	var page inspect.Page
	var source runtimeDiagnosticSource
	if err := json.Unmarshal(out.Output, &page); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(page.Source, &source); err != nil || !source.Partial || page.Total != 1 {
		t.Fatalf("semantic gateway hid blocked actor: %+v (%v)", page, err)
	}
	var value workspaceDiagnostic
	if err := json.Unmarshal(page.Records[0].Value, &value); err != nil || value.ID != attached.WorkspaceID || value.Available || value.Unavailable != "actor_timeout" {
		t.Fatalf("semantic gateway returned false health: %+v (%v)", value, err)
	}
	// Native roots use the same live collaboration/run authority as real model
	// tools, not an external grant or a direct call to the collection helper.
	gateway := &actorControlRuntime{actor: a}
	caller := control.Caller{Kind: control.AgentCaller, AgentID: participant.ID, RunID: run.ID}
	target := control.Target{WorkspaceID: attached.WorkspaceID, SessionID: participant.SessionID}
	if descriptors, err := gateway.Describe(ctx, caller, target, "diagnostics core"); err != nil || len(descriptors) == 0 {
		t.Fatal("native discovery blocked behind unavailable actor", err)
	}
	native := control.Request{ID: "blocked-native-diagnostics", CommandID: "diagnostics.core", Caller: caller, Target: target, Input: input}
	if d, err := gateway.Resolve(ctx, native); err != nil || d.Effect != control.Read {
		t.Fatal("native resolve blocked behind unavailable actor", err)
	}
	nativeResult, err := gateway.Execute(ctx, native)
	if err != nil || nativeResult.Fault != nil || nativeResult.State != control.Committed || len(nativeResult.Output) > 4096 {
		t.Fatalf("native gateway cannot diagnose blocked current actor: %+v (%v)", nativeResult, err)
	}
	if err := json.Unmarshal(nativeResult.Output, &page); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(page.Source, &source); err != nil || !source.Partial || page.Total != 1 {
		t.Fatalf("native gateway hid unavailable owner: %+v (%v)", page, err)
	}
}

func TestDiagnosticWorkspaceDenseGatewayPreservesCanonicalStatusAndRevision(t *testing.T) {
	r, err := New(func(root string) (*workspace.Workspace, error) {
		ws := workspace.NewWorkspace(root, "", "tester", types.Settings{}, coreTestLogger{})
		action := types.StatusAction{ID: "OMIT_ACTION_PAYLOAD", Label: strings.Repeat("action payload ", 9000), Enabled: true}
		for i := range 73 {
			ws.Status.SetIssue(types.IssueSnapshot{ID: fmt.Sprintf("dense-issue-%03d", i), Summary: fmt.Sprintf("Canonical diagnostic %d", i), Detail: strings.Repeat("bounded owner detail ", 190), Actions: []types.StatusAction{action}})
		}
		ws.Status.SetAgent(types.AgentStatusSnapshot{Ref: types.ResourceRef{Kind: types.ResourceAgent, ID: "status-agent"}, Label: strings.Repeat("oversized label ", 9000), Phase: types.AgentIdle, Checkpoint: strings.Repeat("checkpoint ", 9000), CheckpointDetail: strings.Repeat("checkpoint detail ", 9000)})
		ws.Status.SetOperation(types.OperationSnapshot{ID: "status-operation", Label: strings.Repeat("oversized operation ", 9000), State: types.OperationRunning, OutputRef: "OMIT_OUTPUT_REFERENCE", Actions: []types.StatusAction{action}})
		ws.Status.Notify(types.NotificationRecord{ID: "preserve-unread", Summary: "Must remain unread", Source: "diagnostic-fixture"})
		return ws, nil
	}, t.TempDir()+"/receipts.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	c := controlWireClient(t, r, "workspace-observer")
	var attached coreprotocol.AttachResult
	if err := c.Call(t.Context(), "", coreprotocol.Attach, &coreprotocol.AttachRequest{Root: t.TempDir()}, &attached); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(t.Context(), attached.WorkspaceID, coreprotocol.ControlClientRegister, &coreprotocol.ControlClientGrant{Effects: []control.Effect{control.Read}}, nil); err != nil {
		t.Fatal(err)
	}
	a := r.actors()[0]
	var revision uint64
	if err := a.inspect(t.Context(), func() { revision = a.revision }); err != nil {
		t.Fatal(err)
	}
	statusVersion := a.ws.Status.Version()
	issues, notices := a.ws.Status.Issues(), a.ws.Status.Notifications(true)
	agents, operations := a.ws.Status.Agents(), a.ws.Status.Operations()
	execute := func(id, command string, query inspect.Request) inspect.Page {
		t.Helper()
		input, err := json.Marshal(coreprotocol.DiagnosticsInspectRequest{Page: query})
		if err != nil {
			t.Fatal(err)
		}
		var out control.Result
		if _, err := c.CallWith(t.Context(), attached.WorkspaceID, coreprotocol.ControlExecute, &control.Request{ID: id, CommandID: command, Input: input}, &out, coreclient.CallOptions{ID: id}); err != nil {
			t.Fatal(err)
		}
		if out.Fault != nil || out.State != control.Committed || len(out.Output) > 4096 || !json.Valid(out.Output) {
			t.Fatalf("actual gateway evidence not readable/bounded: %+v", out)
		}
		var page inspect.Page
		if err := json.Unmarshal(out.Output, &page); err != nil {
			t.Fatal(err)
		}
		return page
	}
	page := execute("dense-start", "diagnostics.workspace", inspect.Request{View: "issues", Limit: 64, MaxBytes: 4096})
	if page.Total != 73 || page.Next == nil || page.Returned >= 64 {
		t.Fatal("byte budget did not create a real continuation", page)
	}
	seen := map[string]bool{}
	snapshot := page.SnapshotID
	for {
		for _, record := range page.Records {
			var issue types.IssueSnapshot
			if err := json.Unmarshal(record.Value, &issue); err != nil || issue.ID != record.ID || !strings.HasPrefix(issue.Summary, "Canonical diagnostic ") || seen[issue.ID] || len(issue.Actions) != 0 || !strings.Contains(string(record.Value), `"action_count":1`) || strings.Contains(string(record.Value), "OMIT_ACTION_PAYLOAD") {
				t.Fatalf("lost/duplicated canonical issue: %+v (%v)", record, err)
			}
			seen[issue.ID] = true
		}
		if page.Next == nil {
			break
		}
		page = execute(fmt.Sprintf("dense-page-%d", len(seen)), "diagnostics.workspace", *page.Next)
		if page.SnapshotID != snapshot {
			t.Fatal("continuation resampled status")
		}
	}
	if len(seen) != 73 {
		t.Fatal("incomplete dense inventory", len(seen))
	}
	execute("notices-read", "diagnostics.workspace", inspect.Request{View: "notifications", MaxBytes: 4096})
	for _, view := range []string{"agents", "operations"} {
		bounded := execute("bounded-"+view, "diagnostics.workspace", inspect.Request{View: view, MaxBytes: 4096})
		if bounded.Total != 1 || bounded.Returned != 1 || strings.Contains(string(bounded.Records[0].Value), "OMIT_") || !strings.Contains(string(bounded.Records[0].Value), "bytes omitted") {
			t.Fatalf("unbounded status owner field/action export: %+v", bounded)
		}
		if view == "agents" {
			var projected struct{ Label, Checkpoint string }
			if err := json.Unmarshal(bounded.Records[0].Value, &projected); err != nil || len(projected.Label) > 320 || len(projected.Checkpoint) > 320 {
				t.Fatalf("oversized status label/checkpoint not bounded: %v", err)
			}
		}
	}
	execute("core-read", "diagnostics.core", inspect.Request{MaxBytes: 4096})
	var after uint64
	if err := a.inspect(t.Context(), func() { after = a.revision }); err != nil {
		t.Fatal(err)
	}
	if after != revision || a.ws.Status.Version() != statusVersion || !reflect.DeepEqual(issues, a.ws.Status.Issues()) || !reflect.DeepEqual(notices, a.ws.Status.Notifications(true)) || !reflect.DeepEqual(agents, a.ws.Status.Agents()) || !reflect.DeepEqual(operations, a.ws.Status.Operations()) || a.ws.Status.UnreadCount() != 1 {
		t.Fatal("read changed revision, issue detail or canonical notice markers")
	}
}

func TestDiagnosticTypedStalledActorPartialDoesNotAuthorizeDrain(t *testing.T) {
	r := transportRuntime(t)
	c := controlWireClient(t, r, "partial-observer")
	stalled := &actor{id: "stalled-diagnostic-owner", commands: make(chan actorCommand, 128), stop: make(chan struct{})}
	r.mu.Lock()
	r.workspaces[stalled.id] = stalled
	r.mu.Unlock()
	t.Cleanup(func() { r.mu.Lock(); delete(r.workspaces, stalled.id); r.mu.Unlock() })
	start := time.Now()
	var page inspect.Page
	if err := c.Call(t.Context(), "", coreprotocol.CoreDiagnosticsInspect, &coreprotocol.DiagnosticsInspectRequest{Page: inspect.Request{View: "workspaces", MaxBytes: 4096}}, &page); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > time.Second || len(page.Records) != 1 {
		t.Fatal("typed diagnostics stalled or silently omitted actor", page)
	}
	var source runtimeDiagnosticSource
	var value workspaceDiagnostic
	if err := json.Unmarshal(page.Source, &source); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(page.Records[0].Value, &value); err != nil || !source.Partial || value.Available || value.Unavailable != "actor_timeout" {
		t.Fatalf("partial evidence became healthy empty state: %+v %+v %v", source, value, err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if release, err := r.reserveIdle(ctx); err == nil {
		release()
		t.Fatal("diagnostic partial weakened the lifetime reservation fence")
	}
	ctxStatus, cancelStatus := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancelStatus()
	if idle, err := r.Idle(ctxStatus, 0); idle || err == nil {
		t.Fatal("unavailable actor no longer fails closed for idle shutdown", idle, err)
	}
	r.mu.Lock()
	draining, stopping := r.draining, r.stopping
	r.mu.Unlock()
	if draining || stopping || stalled.idleFenced {
		t.Fatal("read/failed reservation changed drain lifecycle")
	}
}

func TestDiagnosticIndependentAdmissionJoinsWorkspaceRetirement(t *testing.T) {
	r, err := New(func(root string) (*workspace.Workspace, error) {
		return workspace.NewWorkspace(root, "", "tester", types.Settings{}, coreTestLogger{}), nil
	}, t.TempDir()+"/receipts.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	c := controlWireClient(t, r, "diagnostic-retirement")
	var attached coreprotocol.AttachResult
	if err := c.Call(t.Context(), "", coreprotocol.Attach, &coreprotocol.AttachRequest{Root: t.TempDir()}, &attached); err != nil {
		t.Fatal(err)
	}
	a := r.actors()[0]
	release, ok := a.beginIndependentRead()
	if !ok {
		t.Fatal("read refused before retirement")
	}
	joined := make(chan struct{})
	go func() { a.close(); close(joined) }()
	<-a.stop
	if extra, ok := a.beginIndependentRead(); ok {
		extra()
		t.Error("new read admitted after retirement")
	}
	if a.ws.Collaboration == nil {
		t.Error("collaboration closed under admitted diagnostic authentication")
	}
	select {
	case <-joined:
		t.Error("workspace retirement outlived no read lease")
	default:
	}
	release()
	select {
	case <-joined:
	case <-time.After(3 * time.Second):
		t.Fatal("retirement did not join settled read")
	}
}
