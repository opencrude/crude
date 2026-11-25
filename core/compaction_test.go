package core

import (
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"crude/agent/provider"
	"crude/agent/session"
	"crude/core/coreprotocol"
	"crude/internal/providersim"
	"crude/types/conversation"
	"crude/types/setting"
	"crude/workspace"
)

func TestCoreCompactionReceiptsFenceReplayAndShutdownCancelsExtraction(t *testing.T) {
	home := t.TempDir()
	setCoreTestHome(t, home)
	t.Setenv("USERPROFILE", home)
	entered := make(chan struct{}, 8)
	release := providersim.NewGate()
	shutdownRelease := providersim.NewGate()
	var extractions atomic.Int32
	server := providersim.New(t)
	server.Respond(func(request providersim.Request) providersim.Turn {
		if string(request.ToolChoice) != `"none"` {
			return providersim.Text("completed")
		}
		// The first compaction is one combined extraction; any later one
		// is held for the shutdown check.
		gate := release
		if extractions.Add(1) > 1 {
			gate = shutdownRelease
		}
		entered <- struct{}{}
		return providersim.Text("<<<intent>>>\nRetain the exact human request.\n<<<work>>>\nAccepted evidence.\n<<<state>>>\nCurrent runtime owners.\n<<<continuation>>>\nNone").WithGate(gate)
	})
	runtime, err := New(func(root string) (*workspace.Workspace, error) {
		w := workspace.NewWorkspace(root, "model", "tester", setting.Settings{}, coreTestLogger{})
		w.SetAppDefaults(setting.App{LocalHistory: setting.LocalHistory{Enabled: "off"}, Memory: setting.Memory{AutoRecall: "off", AutoExtract: "off", Consolidation: "off"}})
		off := false
		cfg := &session.Config{Agents: session.AgentsConfig{ActiveProfile: "default", Profiles: map[string]session.ProfileConfig{"default": {MultiAgent: &off, Categories: map[string]session.CategoryConfig{
			session.CategoryDefault: {AccountID: "test", Model: "model", ModelContextWindow: 200_000, ModelSupportsTools: true},
			session.CategoryErrand:  {AccountID: "test", Model: "model", ModelContextWindow: 200_000, ModelMaxOutput: 2_048},
		}}}}, Accounts: map[string]provider.Account{"test": {ID: "test", ProviderID: "openai", APIKey: "test", Connection: provider.Template{Name: "Test", ProviderURL: server.BaseURL(providersim.Chat), APIStandard: "openai", AuthType: provider.AuthProtocol}}}}
		w.SetBackendConfig(cfg, home)
		return w, nil
	}, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	t.Cleanup(release.Release)
	t.Cleanup(shutdownRelease.Release)
	c, err := runtime.Connect("compaction-client")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	var attached coreprotocol.AttachResult
	if err := coreprotocol.Decode(callCore(t, c, "attach", "", coreprotocol.Attach, &coreprotocol.AttachRequest{Root: t.TempDir()}).Payload, &attached); err != nil {
		t.Fatal(err)
	}
	var created workspace.NewSessionResult
	if err := coreprotocol.Decode(callCore(t, c, "create", attached.WorkspaceID, coreprotocol.NewSession, coreprotocol.InvocationSessionRequest{}).Payload, &created); err != nil {
		t.Fatal(err)
	}
	callCore(t, c, "input", attached.WorkspaceID, coreprotocol.SubmitInput, coreprotocol.InputRequest{AgentID: created.OrchID, Text: strings.Repeat("retained source ", 4_000)})
	snapshot := func() coreprotocol.WorkspaceSnapshot {
		t.Helper()
		var view coreprotocol.WorkspaceSnapshot
		if err := coreprotocol.Decode(callCore(t, c, "snapshot", attached.WorkspaceID, coreprotocol.Snapshot, coreprotocol.Empty{}).Payload, &view); err != nil {
			t.Fatal(err)
		}
		return view
	}
	deadline := time.Now().Add(5 * time.Second)
	for snapshot().Agents[0].Running {
		if time.Now().After(deadline) {
			t.Fatal("initial turn did not settle")
		}
		time.Sleep(time.Millisecond)
	}
	raw, _ := coreprotocol.Marshal(coreprotocol.Ptr(coreprotocol.AgentIDRequest{AgentID: created.OrchID}))
	request := coreprotocol.Request{ID: "compact-once", Owner: c.Owner, WorkspaceID: attached.WorkspaceID, Method: coreprotocol.AgentCompact, Payload: raw}
	done := make(chan coreprotocol.Response, 1)
	go func() { done <- c.Call(t.Context(), request) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("extraction did not start")
	}
	view := snapshot()
	badStop, _ := coreprotocol.Marshal(coreprotocol.Ptr(coreprotocol.AgentRequest{AgentID: created.OrchID, Generation: view.Agents[0].TurnIdentity.Generation, OperationID: "replaced-operation"}))
	if response := c.Call(t.Context(), coreprotocol.Request{ID: "stale-stop", Owner: c.Owner, WorkspaceID: attached.WorkspaceID, Method: coreprotocol.CancelAgent, Payload: badStop}); response.Error == nil || response.Error.Code != "stale_operation" {
		t.Fatalf("stale compaction Stop was accepted: %+v", response)
	}
	release.Release()
	var first coreprotocol.Response
	select {
	case first = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("compaction did not complete")
	}
	if first.Error != nil {
		t.Fatal(first.Error)
	}
	var result conversation.CompactResultMsg
	if err := coreprotocol.Decode(first.Payload, &result); err != nil || result.Error != "" {
		t.Fatalf("compaction result=%+v err=%v", result, err)
	}
	replay := c.Call(t.Context(), request)
	if string(replay.Payload) != string(first.Payload) || snapshot().Agents[0].ActiveCompaction.OperationID != "" {
		t.Fatal("receipt replay reserved a new compaction before checking identity")
	}
	// A new admitted extraction must cancel with actor shutdown, not deadlock
	// Runtime.Close while the actor waits for its independent provider context.
	callCore(t, c, "shutdown-input", attached.WorkspaceID, coreprotocol.SubmitInput, coreprotocol.InputRequest{AgentID: created.OrchID, Text: strings.Repeat("shutdown source ", 4_000)})
	deadline = time.Now().Add(5 * time.Second)
	for snapshot().Agents[0].Running {
		if time.Now().After(deadline) {
			t.Fatal("follow-up turn did not settle")
		}
		time.Sleep(time.Millisecond)
	}
	for len(entered) != 0 {
		<-entered
	}
	request.ID = "compact-before-close"
	closingRequest := request
	go func() { done <- c.Call(t.Context(), closingRequest) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown extraction did not start")
	}
	closed := make(chan error, 1)
	go func() { closed <- runtime.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("core shutdown did not release compaction ownership")
	}
}
