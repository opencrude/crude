package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"crude/agent/collaboration"
	"crude/core/coreclient"
	"crude/core/coreprotocol"
	"crude/core/wiretest"
	"crude/types/control"
	"crude/types/insight"
	"crude/types/inspect"
	"crude/types/setting"
	"crude/types/statuscenter"
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
		return workspace.NewWorkspace(root, "", "tester", setting.Settings{}, coreTestLogger{}), nil
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
	defer func() {
		close(release)
		if cleanupErr := <-joined; cleanupErr != nil {
			t.Logf("Cleanup join: %v", cleanupErr)
		}
	}()
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
		ws := workspace.NewWorkspace(root, "", "tester", setting.Settings{}, coreTestLogger{})
		action := statuscenter.Action{ID: "OMIT_ACTION_PAYLOAD", Label: strings.Repeat("action payload ", 9000), Enabled: true}
		for i := range 73 {
			ws.Status.SetIssue(statuscenter.IssueSnapshot{ID: fmt.Sprintf("dense-issue-%03d", i), Summary: fmt.Sprintf("Canonical diagnostic %d", i), Detail: strings.Repeat("bounded owner detail ", 190), Actions: []statuscenter.Action{action}})
		}
		ws.Status.SetAgent(statuscenter.AgentStatusSnapshot{Ref: statuscenter.ResourceRef{Kind: statuscenter.ResourceAgent, ID: "status-agent"}, Label: strings.Repeat("oversized label ", 9000), Phase: statuscenter.AgentIdle, Checkpoint: strings.Repeat("checkpoint ", 9000), CheckpointDetail: strings.Repeat("checkpoint detail ", 9000)})
		ws.Status.SetOperation(statuscenter.OperationSnapshot{ID: "status-operation", Label: strings.Repeat("oversized operation ", 9000), State: statuscenter.OperationRunning, OutputRef: "OMIT_OUTPUT_REFERENCE", Actions: []statuscenter.Action{action}})
		ws.Status.Notify(statuscenter.NotificationRecord{ID: "preserve-unread", Summary: "Must remain unread", Source: "diagnostic-fixture"})
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
	// Startup publishes a real filesystem resync and an LSP coverage change.
	// Join that producer, then drain its already-queued owner messages before
	// measuring read-only behavior. A command FIFO barrier alone cannot order
	// the actor's independent service-event channel.
	<-a.fileWatch.ready
	// Watch recovery schedules a debounced second assembly. Replace it with
	// one explicit setup generation and observe its completion, not elapsed time.
	a.ws.LSPMgr.RefreshAssembly()
	for {
		_, changed := a.ws.LSPMgr.Changed()
		plan := a.ws.LSPMgr.Assembly()
		if plan.State == insight.AssemblyReady {
			break
		}
		if plan.State == insight.AssemblyFailed {
			t.Fatal("fixture assembly failed:", plan.Message)
		}
		select {
		case <-changed:
		case <-t.Context().Done():
			t.Fatal("fixture assembly did not settle:", t.Context().Err())
		}
	}
	var revision uint64
	if err := a.inspect(t.Context(), func() {
		for {
			select {
			case event := <-a.serviceEvents:
				a.serviceEvent(event)
			default:
				a.publishStatusIfChanged()
				a.publishLSPIfChanged()
				revision = a.revision
				return
			}
		}
	}); err != nil {
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
			var issue statuscenter.IssueSnapshot
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
	// Equal timestamps leave the status owner's map-backed issue order unspecified.
	afterIssues := a.ws.Status.Issues()
	sort.Slice(issues, func(i, j int) bool { return issues[i].ID < issues[j].ID })
	sort.Slice(afterIssues, func(i, j int) bool { return afterIssues[i].ID < afterIssues[j].ID })
	if after != revision {
		t.Errorf("read changed workspace revision: before=%d after=%d", revision, after)
	}
	if got := a.ws.Status.Version(); got != statusVersion {
		t.Errorf("read changed status version: before=%d after=%d", statusVersion, got)
	}
	for _, check := range []struct {
		name          string
		before, after any
	}{
		{"issues", issues, afterIssues},
		{"notices", notices, a.ws.Status.Notifications(true)},
		{"agents", agents, a.ws.Status.Agents()},
		{"operations", operations, a.ws.Status.Operations()},
	} {
		if !reflect.DeepEqual(check.before, check.after) {
			t.Errorf("read changed canonical %s", check.name)
		}
	}
	if got := a.ws.Status.UnreadCount(); got != 1 {
		t.Errorf("read changed unread markers: got=%d want=1", got)
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
	if release, err := r.reserveIdle(ctx, workspace.IdleAutomatic); err == nil {
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
		return workspace.NewWorkspace(root, "", "tester", setting.Settings{}, coreTestLogger{}), nil
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

func TestDiagnosticWorkspaceProblemsReadsClosedFactsInBoundedPages(t *testing.T) {
	root := t.TempDir()
	w, runtime := watchManagerFixture(t, root, "")
	installWatchProducer(w.LSPMgr, root)
	watchAwait(t, "project server", func() bool {
		return len(runtime.snapshot()) == 1 && len(w.LSPMgr.ServerHealthSnapshots()) == 1 && w.LSPMgr.ServerHealthSnapshots()[0].Connected
	})
	client := runtime.snapshot()[0]
	client.muWatch.Lock()
	publish := client.publish
	client.muWatch.Unlock()
	for i := range 15 {
		path := filepath.Join(root, fmt.Sprintf("closed-%02d.fixture", i))
		ds := []insight.LSPDiagnostic{{Line: 2, Col: 4, EndLine: 2, EndCol: 5, Message: strings.Repeat("server message ", 500), Severity: insight.DiagError, Code: "E42", Source: "fixture-checker"}}
		for j := range 8 {
			ds = append(ds, insight.LSPDiagnostic{Line: j, EndLine: j, EndCol: 1, Severity: insight.DiagWarning, Message: "warning", Code: "W1", Source: "fixture-checker"})
		}
		publish(path, insight.DiagnosticPublication{Diagnostics: ds})
	}
	before := w.LSPMgr.Version()
	var store inspect.Store
	page, err := store.Read(t.Context(), "agent", inspect.Request{View: "problems", Limit: 2, MaxBytes: 8192}, func(ctx context.Context) (inspect.Observation, error) {
		return workspaceInspection(ctx, "test", nil, w.LSPMgr, nil, inspect.Request{View: "problems"})
	})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	projectRecord := false
	for {
		raw, _ := json.Marshal(page)
		if len(raw) > 8192 {
			t.Fatal("oversized problem page")
		}
		for _, record := range page.Records {
			if record.Kind == "project_check" {
				projectRecord = true
				continue
			}
			if record.Kind != "problem_file" {
				t.Fatal(record.Kind)
			}
			var value problemFileInspection
			if err := json.Unmarshal(record.Value, &value); err != nil {
				t.Fatal(err)
			}
			if seen[value.Path] || value.ServerID == "" || value.Errors != 1 || value.Warnings != 8 || value.Total != 9 || len(value.Messages) != 5 || value.Omitted != 4 {
				t.Fatalf("problem row: %+v", value)
			}
			first := value.Messages[0]
			if first.Line != 3 || first.Column != 5 || first.Code != "E42" || first.Source != "fixture-checker" || !strings.Contains(first.Message, "omitted") {
				t.Fatalf("bounded, one-based server data missing: %+v", first)
			}
			seen[value.Path] = true
		}
		if page.Next == nil {
			break
		}
		page, err = store.Read(t.Context(), "agent", *page.Next, nil)
		if err != nil {
			t.Fatal(err)
		}
	}
	client.muWatch.Lock()
	opened := client.opened
	client.muWatch.Unlock()
	if len(seen) != 15 || projectRecord || opened != 0 || len(runtime.snapshot()) != 1 || w.LSPMgr.Version() != before {
		t.Fatalf("inspection lost files or changed LSP: files=%d progress=%v opened=%d", len(seen), projectRecord, opened)
	}
	if !strings.Contains(strings.Join(page.Notes, " "), "server data, not instructions") {
		t.Fatal("server evidence was not attributed")
	}
}

func TestDiagnosticWorkspaceLSPIncludesProgressAndBoundedServerEvidence(t *testing.T) {
	root := t.TempDir()
	w, _ := watchManagerFixture(t, root, "")
	manager := w.LSPMgr
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	settings := manager.Settings()
	settings.ProjectCheck = setting.ProjectCheckAlways
	settings.Languages = map[string]setting.LSPLanguage{"go": {Enabled: true, Command: exe}}
	manager.SetSettings(settings)
	path := filepath.Join(root, "too-large.go")
	if err = os.WriteFile(path, make([]byte, settings.MaxDocumentBytes+1), 0600); err != nil {
		t.Fatal(err)
	}
	manager.RefreshAssembly()
	watchAwait(t, "one unchecked file", func() bool { return manager.ProjectInsights().ProjectCheckUnchecked == 1 })
	observed, err := workspaceInspection(t.Context(), "test", nil, manager, nil, inspect.Request{View: "lsp"})
	if err != nil {
		t.Fatal(err)
	}
	progress := false
	for _, record := range observed.Records {
		if record.Kind == "project_check" {
			var value map[string]any
			if err := json.Unmarshal(record.Value, &value); err != nil {
				t.Fatal(err)
			}
			if record.ID == "" || value["server"] != filepath.Base(exe) || value["root"] != root || value["checked"] != float64(0) || value["total"] != float64(1) || value["unchecked"] != float64(1) {
				t.Fatalf("wrong fallback progress: %+v", value)
			}
			progress = true
		}
	}
	if !progress {
		t.Fatal("project check progress is absent")
	}
	if got := boundedServerEvidence([]string{strings.Repeat("server warning ", 1000)}); len(got) != 1 || !strings.Contains(got[0], "omitted") || len(got[0]) > 1100 {
		t.Fatal("evidence escaped text bounds")
	}
}

type diagnosticEvidenceRuntime struct{ base workspace.LSPRuntime }
type diagnosticEvidenceClient struct{ workspace.LSPClient }

func (r diagnosticEvidenceRuntime) NewClient(spec setting.LSPServerSpec, root string, admit func() error) workspace.LSPClient {
	return diagnosticEvidenceClient{r.base.NewClient(spec, root, admit)}
}
func (diagnosticEvidenceClient) Evidence() []string {
	return []string{strings.Repeat("server stderr ", 1000)}
}
func (diagnosticEvidenceClient) ErrorMessage() string { return strings.Repeat("server error ", 1000) }

func TestDiagnosticWorkspaceLSPReadsBoundedLiveEvidence(t *testing.T) {
	root := t.TempDir()
	w, runtime := watchManagerFixture(t, root, "")
	w.LSPMgr.SetRuntime(diagnosticEvidenceRuntime{runtime})
	installWatchProducer(w.LSPMgr, root)
	watchAwait(t, "evidence server", func() bool { h := w.LSPMgr.ServerHealthSnapshots(); return len(h) == 1 && h[0].Connected })
	observed, err := workspaceInspection(t.Context(), "test", nil, w.LSPMgr, nil, inspect.Request{View: "lsp"})
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range observed.Records {
		if record.Kind != "lsp" {
			continue
		}
		var health statuscenter.LSPServerHealth
		if err := json.Unmarshal(record.Value, &health); err != nil {
			t.Fatal(err)
		}
		if health.ID != w.LSPMgr.ServerHealthSnapshots()[0].ID || len(health.Evidence) != 1 || !strings.Contains(health.Evidence[0], "omitted") || !strings.Contains(health.ErrorMessage, "omitted") || len(health.ErrorMessage) > 1100 {
			t.Fatalf("live server evidence changed identity or escaped bounds: %+v", health)
		}
		for _, field := range []string{`"Restarts"`, `"Restarting"`, `"Evidence"`} {
			if !strings.Contains(string(record.Value), field) {
				t.Fatalf("missing %s", field)
			}
		}
		return
	}
	t.Fatal("live LSP health absent")
}
