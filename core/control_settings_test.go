package core

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"crude/coreprotocol"
	"crude/types"
	"crude/types/control"
	"crude/workspace"
)

func TestControlSettingsApplicationCaptureCommitApply(t *testing.T) {
	ws := workspace.NewWorkspace(t.TempDir(), "", "tester", types.Settings{}, coreTestLogger{})
	defer ws.Close()
	before := types.AppSettings{Editor: types.EditorSettings{WordWrap: true, TabSize: 4}}
	ws.SetAppDefaults(before)
	commits := 0
	ws.OnAppPreferencesCommit = func(old, next types.AppSettings) (types.AppSettings, error) {
		commits++
		if !old.Editor.WordWrap || next.Editor.WordWrap || next.Editor.TabSize != 4 {
			t.Fatal("not a single-field change")
		}
		return next, nil
	}
	a := &actor{ws: ws}
	raw, _ := json.Marshal(map[string]any{"scope": "application", "path": "editor.word_wrap", "value": false, "expected_revision": entityRevision(ws.AppDefaults())})
	in := control.Request{ID: "patch", CommandID: "settings.set", Input: raw}
	run, apply, handled := a.prepareControlDomain(in, coreprotocol.Owner{ClientID: "trusted", Epoch: 1})
	if !handled || commits != 0 {
		t.Fatal("prepare performed persistence")
	}
	got := run(context.Background())
	if got.State != control.Committed || got.OperationID != in.ID || commits != 1 {
		t.Fatalf("not committed: %#v", got)
	}
	if !ws.AppDefaults().Editor.WordWrap {
		t.Fatal("worker installed live projection")
	}
	apply(got)
	if ws.AppDefaults().Editor.WordWrap {
		t.Fatal("actor did not publish committed value")
	}
	apply(got)
	run(context.Background())
	if commits != 1 {
		t.Fatal("replayed mutation")
	}
	stale, _, _ := a.prepareControlDomain(in, coreprotocol.Owner{ClientID: "trusted", Epoch: 1})
	if out := stale(context.Background()); out.Fault == nil || out.Fault.Code != control.Stale {
		t.Fatal("stale preimage accepted")
	}
}

func TestControlSettingsRejectAuthorityAndInvalidLeaves(t *testing.T) {
	ws := workspace.NewWorkspace(t.TempDir(), "", "tester", types.Settings{}, coreTestLogger{})
	defer ws.Close()
	a := &actor{ws: ws}
	for _, path := range []string{"accounts.one", "network.proxy_url", "agents", "safety.confirm_at", "capabilities", "terminal.environment", "value.APIKey"} {
		raw, _ := json.Marshal(map[string]any{"scope": "workspace", "path": path, "value": false, "expected_revision": "old"})
		run, _, _ := a.prepareControlDomain(control.Request{ID: "denied", CommandID: "settings.set", Input: raw}, coreprotocol.Owner{ClientID: "trusted", Epoch: 1})
		if out := run(context.Background()); out.Fault == nil || out.Fault.Code != control.Denied {
			t.Fatalf("authority %s accepted: %#v", path, out)
		}
	}
	for _, raw := range []string{`"false"`, `null`, `{"unknown":true}`} {
		value := types.AppSettings{}
		if err := controlSetField(&value, "editor.word_wrap", json.RawMessage(raw)); err == nil {
			t.Fatalf("invalid bool accepted: %s", raw)
		}
	}
	value := types.AppSettings{}
	if err := controlSetField(&value, "editor.WordWrap", json.RawMessage(`false`)); err == nil {
		t.Fatal("case alias accepted")
	}
}

func TestControlSparseFalseAndResetPreserveNeighbors(t *testing.T) {
	tree := map[string]any{"editor": map[string]any{"tab_size": float64(8)}, "theme": map[string]any{"preset": "dark"}}
	controlPatch(tree, []string{"editor", "word_wrap"}, false, false)
	raw, _ := json.Marshal(tree)
	if !strings.Contains(string(raw), `"word_wrap":false`) {
		t.Fatal("false became inheritance")
	}
	controlPatch(tree, []string{"editor", "word_wrap"}, nil, true)
	raw, _ = json.Marshal(tree)
	if strings.Contains(string(raw), "word_wrap") || !strings.Contains(string(raw), "tab_size") || !strings.Contains(string(raw), "dark") {
		t.Fatal("reset removed neighbors")
	}
}

func TestControlSessionRootPinResetPreservesOtherRoleFields(t *testing.T) {
	a := controlExecutionFixture(t)
	var sessionID string
	for _, front := range a.ws.Sessions {
		sessionID = front.SessionID
		break
	}
	backend := a.ws.BackendSession(sessionID)
	initial := backend.SettingsSnapshots()
	value := initial.Effective
	value.SessionMainAgent = "crafter"
	value.SessionOverrides = map[string]types.CategorySettings{
		"default":      {AccountID: "one", Model: "neighbor"},
		"root/crafter": {AccountID: "one", Model: "pinned", ThinkingEffort: "high"},
	}
	if _, err := backend.SaveSettingsSnapshotAtRevision(value, initial.Revision); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"Model", "ThinkingEffort", "account_id"} {
		raw, _ := json.Marshal(map[string]any{"scope": "session", "path": "value." + field, "session_revision": backend.SettingsSnapshots().Revision})
		run, apply, _ := a.prepareControlDomain(control.Request{ID: "reset-" + field, CommandID: "settings.reset", Target: control.Target{SessionID: sessionID}, Input: raw}, coreprotocol.Owner{ClientID: "trusted", Epoch: 1})
		out := run(context.Background())
		if out.Fault != nil {
			t.Fatal(field, out.Fault)
		}
		if apply != nil {
			apply(out)
		}
		snap := backend.SettingsSnapshots()
		pin := snap.Overrides["root/crafter"]
		if snap.Effective.SessionMainAgent != "crafter" || snap.Overrides["default"].Model != "neighbor" {
			t.Fatal("reset switched root or lost neighboring role")
		}
		if field == "Model" && (pin.Model != "" || pin.AccountID != "one" || pin.ThinkingEffort != "high") {
			t.Fatal("model reset not sparse", pin)
		}
		if field == "ThinkingEffort" && (pin.ThinkingEffort != "" || pin.AccountID != "one") {
			t.Fatal("thinking reset cleared connection", pin)
		}
		if field == "account_id" && pin.AccountID != "" {
			t.Fatal("account reset retained pin", pin)
		}
	}
}

// Agents may turn code intelligence on for their session through settings.set,
// and the change reaches the live session; reset returns it to inheritance.
func TestControlSessionCodeIntelligenceSetAndReset(t *testing.T) {
	a := controlExecutionFixture(t)
	var sessionID string
	for _, front := range a.ws.Sessions {
		sessionID = front.SessionID
		break
	}
	backend := a.ws.BackendSession(sessionID)
	call := func(command string, input map[string]any) control.Result {
		t.Helper()
		input["scope"], input["path"], input["session_revision"] = "session", "code_intelligence", backend.SettingsSnapshots().Revision
		raw, _ := json.Marshal(input)
		run, apply, _ := a.prepareControlDomain(control.Request{ID: command, CommandID: command, Target: control.Target{SessionID: sessionID}, Input: raw}, coreprotocol.Owner{ClientID: "trusted", Epoch: 1})
		out := run(context.Background())
		if apply != nil {
			apply(out)
		}
		return out
	}
	if out := call("settings.set", map[string]any{"value": true}); out.Fault != nil {
		t.Fatal(out.Fault)
	}
	if got := backend.SettingsSnapshots().Effective.SessionCodeIntelligence; got == nil || !*got {
		t.Fatalf("session override after set = %v", got)
	}
	if out := call("settings.reset", map[string]any{}); out.Fault != nil {
		t.Fatal(out.Fault)
	}
	if got := backend.SettingsSnapshots().Effective.SessionCodeIntelligence; got != nil {
		t.Fatalf("session override after reset = %v", *got)
	}
	raw, _ := json.Marshal(map[string]any{"scope": "workspace", "path": "lsp.languages.go.command", "value": "/tmp/evil", "expected_revision": "old"})
	run, _, _ := a.prepareControlDomain(control.Request{ID: "server", CommandID: "settings.set", Input: raw}, coreprotocol.Owner{ClientID: "trusted", Epoch: 1})
	if out := run(context.Background()); out.Fault == nil || out.Fault.Code != control.Denied {
		t.Fatalf("server command change accepted: %#v", out)
	}
}
