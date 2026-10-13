package core

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"crude/coreprotocol"
	"crude/document"
	"crude/provider"
	"crude/session"
	"crude/types"
	"crude/types/control"
	"crude/workspace"
	"crude/worktree"
)

func controlExecutionFixture(t *testing.T) *actor {
	t.Helper()
	root := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git: %v %s", err, out)
		}
	}
	git("init", "-q")
	git("config", "user.name", "test")
	git("config", "user.email", "test@example.invalid")
	if err := os.WriteFile(filepath.Join(root, "live.txt"), []byte("one\ntwo\nthree\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "disk.txt"), []byte("base\n"), 0644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-qm", "base")
	if err := os.WriteFile(filepath.Join(root, "disk.txt"), []byte("human dirty\n"), 0644); err != nil {
		t.Fatal(err)
	}
	ws := workspace.NewWorkspace(root, "", "test", types.Settings{}, coreTestLogger{})
	t.Cleanup(ws.Close)
	cfg := &session.Config{Accounts: map[string]provider.Account{"one": {ID: "one", ProviderID: "openai", APIKey: "secret", Connection: provider.Template{Name: "test", AuthType: provider.AuthBearer, APIStandard: "openai", ProviderURL: "https://example.invalid"}}}, Agents: session.AgentsConfig{ActiveProfile: "default", Profiles: map[string]session.ProfileConfig{"default": {Categories: map[string]session.CategoryConfig{"default": {AccountID: "one", Model: "base"}, "crafter": {AccountID: "one", Model: "role-base"}}}}}}
	if _, err := ws.CreateSession(cfg, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	return &actor{ws: ws}
}

func TestControlExecutionRealDocumentIntegration(t *testing.T) {
	a := controlExecutionFixture(t)
	ctx := context.Background()
	owner := coreprotocol.Owner{ClientID: "trusted", Epoch: 1}
	call := func(command, id string, input any) control.Result {
		t.Helper()
		raw, _ := json.Marshal(input)
		run, _, ok := a.prepareControlDomain(control.Request{ID: command, CommandID: command, Target: control.Target{ResourceID: id}, Input: raw}, owner)
		if !ok {
			t.Fatal("route missing", command)
		}
		return run(ctx)
	}
	human := document.Owner{ClientID: "human", Epoch: 1}
	snap, err := a.ws.LiveDocuments.Open(human, "live.txt")
	if err != nil {
		t.Fatal(err)
	}
	edited, err := a.ws.LiveDocuments.Apply(document.Change{DocumentID: snap.ID, RequestID: "dirty", Owner: human, BaseRevision: snap.Revision, GroupID: "dirty", Edits: []document.Edit{{Start: 0, End: 3, Text: "ONE"}}})
	if err != nil {
		t.Fatal(err)
	}
	created := call("execution.create", "", map[string]any{"mode": "worktree"})
	if created.Fault != nil {
		t.Fatal(created.Fault)
	}
	var c worktree.Context
	if err = json.Unmarshal(created.Output, &c); err != nil {
		t.Fatal(err)
	}
	if c.Owner.AgentID != "client:trusted" || len(created.ResourceIDs) != 1 {
		t.Fatal("missing trusted identity")
	}
	if len(c.Pins) != 0 || !c.Ready {
		t.Fatal("external allocation retained a nonexistent task pin", c)
	}
	if inherited, e := os.ReadFile(filepath.Join(c.Root, "disk.txt")); e != nil || string(inherited) != "human dirty\n" {
		t.Fatal("dirty Git baseline not inherited", string(inherited), e)
	}
	if err = os.WriteFile(filepath.Join(c.Root, "live.txt"), []byte("ONE\nTWO\nthree\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = a.ws.LiveDocuments.Apply(document.Change{DocumentID: snap.ID, RequestID: "later", Owner: human, BaseRevision: edited.Revision, GroupID: "later", Edits: []document.Edit{{Start: 8, End: 13, Text: "THREE"}}}); err != nil {
		t.Fatal(err)
	}
	reviewed := call("execution.review", c.ID, map[string]any{"id": c.ID})
	if reviewed.Fault != nil {
		t.Fatal(reviewed.Fault)
	}
	var plan worktree.PlanDelta
	if err = json.Unmarshal(reviewed.Output, &plan); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"plan": plan})
	run, _, _ := a.prepareControlDomain(control.Request{ID: "apply", CommandID: "execution.apply", Target: control.Target{ResourceID: c.ID}, Input: raw}, owner)
	applied := run(ctx)
	if applied.Fault != nil {
		t.Fatal(applied.Fault)
	}
	if string(run(ctx).Output) != string(applied.Output) {
		t.Fatal("lost receipt replayed")
	}
	recovered := call("execution.apply", c.ID, map[string]any{"plan": plan})
	if recovered.Fault != nil || string(recovered.Output) != string(applied.Output) {
		t.Fatal("fresh lost-reply lookup did not return exact receipt", recovered)
	}
	data, err := os.ReadFile(filepath.Join(a.ws.RootPath, "live.txt"))
	if err != nil || string(data) != "one\ntwo\nthree\n" {
		t.Fatalf("dirty text incorrectly forced to disk %q %v", data, err)
	}
	accepted, err := a.ws.LiveDocuments.Snapshot(snap.ID)
	if err != nil || string(accepted.Text) != "ONE\nTWO\nTHREE\n" || !accepted.Dirty {
		t.Fatalf("lost accepted human change: %#v %v", accepted, err)
	}
	var receipt worktree.Receipt
	if err = json.Unmarshal(applied.Output, &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.SafetySnapshotID == "" || len(applied.ResourceIDs) != 2 {
		t.Fatal("missing exact integration evidence")
	}
	undone := call("execution.undo", receipt.OperationID, map[string]any{"operation_id": receipt.OperationID})
	if undone.Fault != nil {
		t.Fatal(undone.Fault)
	}
	data, err = os.ReadFile(filepath.Join(a.ws.RootPath, "live.txt"))
	if err != nil || string(data) != "one\ntwo\nthree\n" {
		t.Fatalf("undo disk preimage %q %v", data, err)
	}
	accepted, err = a.ws.LiveDocuments.Snapshot(snap.ID)
	if err != nil || string(accepted.Text) != "ONE\ntwo\nTHREE\n" {
		t.Fatalf("undo lost human changes: %#v %v", accepted, err)
	}
	if inherited, e := os.ReadFile(filepath.Join(a.ws.RootPath, "disk.txt")); e != nil || string(inherited) != "human dirty\n" {
		t.Fatal("integration/undo changed inherited dirty Git bytes", string(inherited), e)
	}
	integrations := call("execution.integrations", "", struct{}{})
	if integrations.Fault != nil {
		t.Fatal(integrations.Fault)
	}
}

func TestControlExecutionSelectionExactSessionAndRevision(t *testing.T) {
	a := controlExecutionFixture(t)
	sid := a.ws.Sessions[0].SessionID
	owner := coreprotocol.Owner{ClientID: "trusted", Epoch: 1}
	call := func(command, sessionID, resource string, input any) control.Result {
		t.Helper()
		raw, _ := json.Marshal(input)
		run, _, _ := a.prepareControlExecution(control.Request{ID: command, CommandID: command, Target: control.Target{SessionID: sessionID, ResourceID: resource}, Input: raw}, owner)
		return run(t.Context())
	}
	created := call("execution.create", "", "", map[string]any{"mode": "worktree"})
	if created.Fault != nil {
		t.Fatal(created.Fault)
	}
	var c worktree.Context
	if err := json.Unmarshal(created.Output, &c); err != nil {
		t.Fatal(err)
	}
	selected := call("execution.select", sid, c.ID, map[string]any{"id": c.ID, "expected_revision": 0})
	if selected.Fault != nil || selected.Revision == nil || *selected.Revision != 1 {
		t.Fatal(selected)
	}
	got := call("execution.selection", sid, "", struct{}{})
	if got.Fault != nil || string(got.Output) != string(selected.Output) {
		t.Fatal(got)
	}
	for _, test := range []struct {
		session, resource string
		input             map[string]any
		code              control.FaultCode
	}{
		{sid, c.ID, map[string]any{"id": c.ID, "expected_revision": 0}, control.Stale},
		{sid, "wrong", map[string]any{"id": c.ID, "expected_revision": 1}, control.Invalid},
		{"", c.ID, map[string]any{"id": c.ID, "expected_revision": 1}, control.Invalid},
		{"foreign", c.ID, map[string]any{"id": c.ID, "expected_revision": 1}, control.Unavailable},
		{sid, c.ID, map[string]any{"id": c.ID}, control.Invalid},
		{sid, "", map[string]any{"expected_revision": 1}, control.Invalid},
	} {
		out := call("execution.select", test.session, test.resource, test.input)
		if out.Fault == nil || out.Fault.Code != test.code {
			t.Fatal("unsafe selection", out)
		}
	}
	shared := call("execution.select", sid, "", map[string]any{"id": "", "expected_revision": 1})
	if shared.Fault != nil || shared.Revision == nil || *shared.Revision != 2 {
		t.Fatal(shared)
	}
	artifact, err := a.ws.Executions.Inspect(c.ID)
	if err != nil || len(artifact.Pins) != 0 {
		t.Fatal("unexecuted allocation has no lease cleanup path", artifact, err)
	}
}

func TestControlExecutionRejectsIdentityAndOwnerForgery(t *testing.T) {
	a := controlExecutionFixture(t)
	for _, in := range []control.Request{
		{CommandID: "execution.create", Input: json.RawMessage(`{"mode":"worktree","owner":{"agent_id":"other","run_id":"run"}}`)},
		{CommandID: "execution.inspect", Target: control.Target{ResourceID: "two"}, Input: json.RawMessage(`{"id":"one"}`)},
	} {
		run, _, _ := a.prepareControlDomain(in, coreprotocol.Owner{ClientID: "trusted", Epoch: 1})
		if out := run(context.Background()); out.Fault == nil || out.Fault.Code != control.Invalid {
			t.Fatal("unsafe identity", out)
		}
	}
}

func TestControlExecutionStaleReviewDoesNotApply(t *testing.T) {
	a := controlExecutionFixture(t)
	owner := coreprotocol.Owner{ClientID: "trusted", Epoch: 1}
	call := func(command, id string, input any) control.Result {
		raw, _ := json.Marshal(input)
		run, _, _ := a.prepareControlDomain(control.Request{ID: command, CommandID: command, Target: control.Target{ResourceID: id}, Input: raw}, owner)
		return run(context.Background())
	}
	created := call("execution.create", "", map[string]any{"mode": "worktree"})
	if created.Fault != nil {
		t.Fatal(created.Fault)
	}
	var c worktree.Context
	if err := json.Unmarshal(created.Output, &c); err != nil {
		t.Fatal(err)
	}
	put := func(text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(c.Root, "live.txt"), []byte(text), 0644); err != nil {
			t.Fatal(err)
		}
	}
	put("one\nTWO\nthree\n")
	reviewed := call("execution.review", c.ID, map[string]any{"id": c.ID})
	if reviewed.Fault != nil {
		t.Fatal(reviewed.Fault)
	}
	var plan worktree.PlanDelta
	if err := json.Unmarshal(reviewed.Output, &plan); err != nil {
		t.Fatal(err)
	}
	put("one\nDIFFERENT\nthree\n")
	failed := call("execution.apply", c.ID, map[string]any{"plan": plan})
	if failed.Fault == nil || failed.Fault.Code != control.Stale || failed.State != control.Failed {
		t.Fatal("stale source accepted", failed)
	}
	data, err := os.ReadFile(filepath.Join(a.ws.RootPath, "live.txt"))
	if err != nil || string(data) != "one\ntwo\nthree\n" {
		t.Fatal("stale apply changed checkout", string(data), err)
	}
	raw, _ := json.Marshal(map[string]any{"id": c.ID})
	run, _, _ := a.prepareControlDomain(control.Request{CommandID: "execution.keep", Caller: control.Caller{Kind: control.AgentCaller, AgentID: "foreign", RunID: "foreign-run"}, Target: control.Target{ResourceID: c.ID}, Input: raw}, owner)
	if result := run(context.Background()); result.Fault == nil || result.Fault.Code != control.Denied {
		t.Fatal("foreign run authority accepted", result)
	}
}
