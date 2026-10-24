package core

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"crude/coreprotocol"
	"crude/provider"
	"crude/session"
	"crude/types"
	"crude/workspace"
)

func TestCoreCompactionReceiptsFenceReplayAndShutdownCancelsExtraction(t *testing.T) {
	home := t.TempDir()
	setCoreTestHome(t, home)
	t.Setenv("USERPROFILE", home)
	entered := make(chan struct{}, 8)
	release := make(chan struct{})
	shutdownRelease := make(chan struct{})
	var extractions atomic.Int32
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in provider.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			t.Error(err)
			return
		}
		text := "completed"
		if in.ToolChoice == "none" {
			releaseRequest := release
			// The first compaction is one combined extraction; any later one
			// is held for the shutdown check.
			if extractions.Add(1) > 1 {
				releaseRequest = shutdownRelease
			}
			entered <- struct{}{}
			select {
			case <-releaseRequest:
			case <-r.Context().Done():
				return
			}
			text = "<<<intent>>>\nRetain the exact human request.\n<<<work>>>\nAccepted evidence.\n<<<state>>>\nCurrent runtime owners.\n<<<continuation>>>\nNone"
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", text)
	}))
	t.Cleanup(server.Close)
	runtime, err := New(func(root string) (*workspace.Workspace, error) {
		w := workspace.NewWorkspace(root, "model", "tester", types.Settings{}, coreTestLogger{})
		w.SetAppDefaults(types.AppSettings{LocalHistory: types.LocalHistorySettings{Enabled: "off"}, Memory: types.MemorySettings{AutoRecall: "off", AutoExtract: "off", Consolidation: "off"}})
		off := false
		cfg := &session.Config{Agents: session.AgentsConfig{ActiveProfile: "default", Profiles: map[string]session.ProfileConfig{"default": {MultiAgent: &off, Categories: map[string]session.CategoryConfig{
			session.CategoryDefault: {AccountID: "test", Model: "model", ModelContextWindow: 200_000, ModelSupportsTools: true},
			session.CategoryAeron:   {AccountID: "test", Model: "model", ModelContextWindow: 200_000, ModelMaxOutput: 2_048},
		}}}}, Accounts: map[string]provider.Account{"test": {ID: "test", ProviderID: "openai", APIKey: "test", Connection: provider.Template{Name: "Test", ProviderURL: server.URL, APIStandard: "openai", AuthType: provider.AuthProtocol}}}}
		w.SetBackendConfig(cfg, home)
		return w, nil
	}, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	t.Cleanup(func() { close(shutdownRelease) })
	c, err := runtime.Connect("compaction-client")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	var attached coreprotocol.AttachResult
	if err := coreprotocol.Decode(callCore(t, c, "attach", "", coreprotocol.Attach, coreprotocol.AttachRequest{Root: t.TempDir()}).Payload, &attached); err != nil {
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
	once.Do(func() { close(release) })
	var first coreprotocol.Response
	select {
	case first = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("compaction did not complete")
	}
	if first.Error != nil {
		t.Fatal(first.Error)
	}
	var result types.CompactResultMsg
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
