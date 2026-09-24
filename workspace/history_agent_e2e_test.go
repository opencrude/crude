package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"crude/localhistory"
	"crude/provider"
	"crude/session"
	ttypes "crude/types"
)

type historyE2EEvent struct {
	typ     string
	agentID string
	data    any
}

type historyE2ESender struct{ events chan historyE2EEvent }

func (s *historyE2ESender) SendAgentEvent(typ string, data any) {
	s.events <- historyE2EEvent{typ: typ, data: data}
}

func (s *historyE2ESender) SendAgentEventWithID(typ string, data any, agentID string) {
	s.events <- historyE2EEvent{typ: typ, data: data, agentID: agentID}
}

func historyE2EConfig(url string) *session.Config {
	off := false
	return &session.Config{
		ActiveProvider: "openai",
		Accounts:       map[string]provider.Account{"test-account": {ID: "test-account", ProviderID: "openai", Label: "Test", APIKey: "test"}},
		Providers: map[string]session.ProviderConfig{"openai": {
			Name: "OpenAI", ProviderURL: url, APIStandard: "openai", AuthType: provider.AuthProtocol,
		}},
		Agents: session.AgentsConfig{Enabled: false, ActiveProfile: "default", Profiles: map[string]session.ProfileConfig{
			"default": {MultiAgent: &off, Categories: map[string]session.CategoryConfig{
				session.CategoryDefault: {AccountID: "test-account", Model: "history-model"},
			}},
		}},
	}
}

func waitHistoryAgentDone(t *testing.T, sender *historyE2ESender, agentID string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case event := <-sender.events:
			if event.typ == "done" && event.agentID == agentID {
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for real agent turn")
		}
	}
}

func waitAgentHistoryScopes(t *testing.T, service *LocalHistoryService, sessionID, agentID string) []localhistory.MessageScope {
	t.Helper()
	var scopes []localhistory.MessageScope
	var err error
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		scopes, err = service.AgentUndoableScopes(sessionID, agentID)
		if err == nil && len(scopes) > 0 {
			return scopes
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("agent scopes=%+v err=%v", scopes, err)
	return nil
}

func TestAgentUndoProductionPathPreservesExternalPreimage(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	project := t.TempDir()
	path := filepath.Join(project, "shared.txt")
	if err := os.WriteFile(path, []byte("a=0\nb=0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if requests.Add(1) == 1 {
			// This write occurs inside the live human-message scope but outside
			// the agent tool. The tool's preimage must preserve it.
			if err := os.WriteFile(path, []byte("a=0\nb=external\n"), 0o644); err != nil {
				t.Errorf("external write: %v", err)
				return
			}
			arguments, _ := json.Marshal(map[string]string{
				"path": path, "old_string": "a=0", "new_string": "a=agent",
			})
			_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_edit\",\"type\":\"function\",\"function\":{\"name\":\"edit_file\",\"arguments\":%q}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n", string(arguments))
			return
		}
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"edited\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()

	cfg := historyE2EConfig(server.URL)
	ws := NewWorkspace(project, "history-model", "tester", ttypes.Settings{}, &testLogger{})
	t.Cleanup(ws.Close)
	ws.SetAppDefaults(ttypes.AppSettings{LocalHistory: ttypes.LocalHistorySettings{Enabled: "on", UseGitIgnore: "on", CaptureExternalChanges: "on", SafetySnapshotBeforeRestore: "on", MaxFileSizeMB: 10}})
	frontend, err := ws.CreateSession(cfg, home)
	if err != nil {
		t.Fatal(err)
	}
	frontend.backend.SetYolo(true)
	sender := &historyE2ESender{events: make(chan historyE2EEvent, 64)}
	orchestrator, err := ws.StartOrchestrator(sender)
	if err != nil {
		t.Fatal(err)
	}
	if !frontend.backend.SubmitInput(orchestrator.ID, provider.PlainInput("edit a only")) {
		t.Fatal("turn was not admitted")
	}
	waitHistoryAgentDone(t, sender, orchestrator.ID)
	scopes := waitAgentHistoryScopes(t, ws.HistoryService, frontend.SessionID, orchestrator.ID)
	if len(scopes) != 1 {
		t.Fatalf("agent scopes=%+v", scopes)
	}
	plan, err := ws.HistoryService.PlanAgentRestore(context.Background(), frontend.SessionID, scopes[0].MessageID, orchestrator.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Changes) != 1 || plan.Changes[0].Path != "shared.txt" || plan.Changes[0].ConflictReason != "" {
		t.Fatalf("agent plan=%+v", plan.Changes)
	}
	result, err := ws.ApplyHistoryRestoreChoices(context.Background(), plan.ID, []localhistory.RestoreChoice{{Path: "shared.txt"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.SessionID != frontend.SessionID || result.MessageID != scopes[0].MessageID || result.AgentID != orchestrator.ID {
		t.Fatalf("restore owner=%+v", result)
	}
	if err := ws.AppendHistoryRestoreEvent(result); err != nil {
		t.Fatal(err)
	}
	persisted := frontend.backend.MessagesWithMeta()
	foundBoundary := false
	for _, row := range persisted {
		for _, snapshot := range row.Meta.Snapshots {
			if snapshot.LifecyclePhase == "history_restored" && snapshot.LifecycleOperationID == result.PlanID && snapshot.HistorySafetySnapshot == result.SafetySnapshotID {
				foundBoundary = true
			}
		}
	}
	if !foundBoundary {
		t.Fatal("restore boundary metadata was not persisted")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != "a=0\nb=external\n" {
		t.Fatalf("production undo destroyed external preimage: %q", got)
	}
	pending, err := ws.HistoryService.PendingRestoreNotifications(frontend.SessionID)
	if err != nil || len(pending) != 0 {
		t.Fatalf("restore notification remains pending: %+v err=%v", pending, err)
	}
}

func TestAgentUndoProductionPathGuardsPostToolExternalOverwrite(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	project := t.TempDir()
	path := filepath.Join(project, "shared.txt")
	if err := os.WriteFile(path, []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if requests.Add(1) == 1 {
			arguments, _ := json.Marshal(map[string]string{"path": path, "content": "agent\n"})
			_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_write\",\"type\":\"function\",\"function\":{\"name\":\"write_file\",\"arguments\":%q}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n", string(arguments))
			return
		}
		if err := os.WriteFile(path, []byte("external after tool\n"), 0o644); err != nil {
			t.Errorf("external overwrite: %v", err)
			return
		}
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"finished\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	cfg := historyE2EConfig(server.URL)
	ws := NewWorkspace(project, "history-model", "tester", ttypes.Settings{}, &testLogger{})
	t.Cleanup(ws.Close)
	ws.SetAppDefaults(ttypes.AppSettings{LocalHistory: ttypes.LocalHistorySettings{Enabled: "on", UseGitIgnore: "on", CaptureExternalChanges: "on", SafetySnapshotBeforeRestore: "on", MaxFileSizeMB: 10}})
	frontend, err := ws.CreateSession(cfg, home)
	if err != nil {
		t.Fatal(err)
	}
	frontend.backend.SetYolo(true)
	sender := &historyE2ESender{events: make(chan historyE2EEvent, 64)}
	orchestrator, err := ws.StartOrchestrator(sender)
	if err != nil {
		t.Fatal(err)
	}
	if !frontend.backend.SubmitInput(orchestrator.ID, provider.PlainInput("write the file")) {
		t.Fatal("turn was not admitted")
	}
	waitHistoryAgentDone(t, sender, orchestrator.ID)
	scopes := waitAgentHistoryScopes(t, ws.HistoryService, frontend.SessionID, orchestrator.ID)
	plan, err := ws.HistoryService.PlanAgentRestore(context.Background(), frontend.SessionID, scopes[0].MessageID, orchestrator.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Changes) != 1 || plan.Changes[0].ConflictReason == "" || plan.Changes[0].AutoMerged {
		t.Fatalf("external overwrite was not guarded: %+v", plan.Changes)
	}
	if _, err := ws.ApplyHistoryRestoreChoices(context.Background(), plan.ID, nil); err == nil {
		t.Fatal("conflicted external overwrite was automatically restored")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != "external after tool\n" {
		t.Fatalf("guarded preview changed external data: %q", got)
	}
}

func TestHistoryStartFailureAbortsBeforeProviderExecution(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	project := t.TempDir()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	ws := NewWorkspace(project, "history-model", "tester", ttypes.Settings{}, &testLogger{})
	t.Cleanup(ws.Close)
	frontend, err := ws.CreateSession(historyE2EConfig(server.URL), home)
	if err != nil {
		t.Fatal(err)
	}
	frontend.backend.SetHistoryHooks(func(context.Context, string, string) error {
		return errors.New("history disk unavailable")
	}, nil)
	sender := &historyE2ESender{events: make(chan historyE2EEvent, 16)}
	orchestrator, err := ws.StartOrchestrator(sender)
	if err != nil {
		t.Fatal(err)
	}
	if !frontend.backend.SubmitInput(orchestrator.ID, provider.PlainInput("must not run")) {
		t.Fatal("turn was not admitted")
	}
	deadline := time.After(3 * time.Second)
	for {
		select {
		case event := <-sender.events:
			if event.typ == "error" {
				if requests.Load() != 0 {
					t.Fatalf("provider requests=%d", requests.Load())
				}
				return
			}
		case <-deadline:
			t.Fatal("history failure did not terminate the turn")
		}
	}
}

func TestEnabledHistoryStoreOpenFailureBlocksProviderAndTools(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	project := t.TempDir()
	owner := NewWorkspace(project, "history-model", "owner", ttypes.Settings{}, &testLogger{})
	t.Cleanup(owner.Close)
	blocked := NewWorkspace(project, "history-model", "blocked", ttypes.Settings{}, &testLogger{})
	t.Cleanup(blocked.Close)
	if blocked.HistoryService != nil || blocked.localHistoryErr == nil {
		t.Fatal("second workspace did not preserve Local History open failure")
	}
	blocked.SetAppDefaults(ttypes.AppSettings{LocalHistory: ttypes.LocalHistorySettings{Enabled: "on"}})
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	frontend, err := blocked.CreateSession(historyE2EConfig(server.URL), home)
	if err != nil {
		t.Fatal(err)
	}
	sender := &historyE2ESender{events: make(chan historyE2EEvent, 16)}
	orchestrator, err := blocked.StartOrchestrator(sender)
	if err != nil {
		t.Fatal(err)
	}
	if !frontend.backend.SubmitInput(orchestrator.ID, provider.PlainInput("must remain blocked")) {
		t.Fatal("turn was not admitted")
	}
	deadline := time.After(3 * time.Second)
	for {
		select {
		case event := <-sender.events:
			if event.typ == "error" {
				if requests.Load() != 0 {
					t.Fatalf("provider requests=%d", requests.Load())
				}
				return
			}
		case <-deadline:
			t.Fatal("unavailable enabled history did not block the turn")
		}
	}
}

func TestDisabledHistoryTurnStillExcludesRestore(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	project := t.TempDir()
	path := filepath.Join(project, "file")
	if err := os.WriteFile(path, []byte("target"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws := NewWorkspace(project, "history-model", "tester", ttypes.Settings{}, &testLogger{})
	t.Cleanup(ws.Close)
	target, err := ws.LocalHistory.Snapshot(context.Background(), localhistory.SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("current"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, err := ws.LocalHistory.PlanRestore(context.Background(), target.ID, localhistory.SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ws.SetAppDefaults(ttypes.AppSettings{LocalHistory: ttypes.LocalHistorySettings{Enabled: "off"}})
	providerStarted := make(chan struct{})
	releaseProvider := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		select {
		case <-providerStarted:
		default:
			close(providerStarted)
		}
		<-releaseProvider
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	frontend, err := ws.CreateSession(historyE2EConfig(server.URL), home)
	if err != nil {
		t.Fatal(err)
	}
	sender := &historyE2ESender{events: make(chan historyE2EEvent, 16)}
	orchestrator, err := ws.StartOrchestrator(sender)
	if err != nil {
		t.Fatal(err)
	}
	if !frontend.backend.SubmitInput(orchestrator.ID, provider.PlainInput("read only turn")) {
		t.Fatal("turn was not admitted")
	}
	select {
	case <-providerStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("provider did not start")
	}
	restoreDone := make(chan error, 1)
	go func() {
		_, err := ws.HistoryService.ApplyRestore(context.Background(), plan.ID, []string{"file"})
		restoreDone <- err
	}()
	select {
	case err := <-restoreDone:
		t.Fatalf("restore crossed disabled-history activity lease: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseProvider)
	waitHistoryAgentDone(t, sender, orchestrator.ID)
	select {
	case err := <-restoreDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("restore did not proceed after disabled-history turn completed")
	}
}

func TestAgentUndoTracksInsideAndOutsideFilesystemMutations(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	project, outside := t.TempDir(), t.TempDir()
	insidePath := filepath.Join(project, "inside.txt")
	outsidePath := filepath.Join(outside, "outside.txt")
	unrelatedPath := filepath.Join(outside, "unrelated.txt")
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if requests.Add(1) == 1 {
			insideArgs, _ := json.Marshal(map[string]string{"path": "inside.txt", "content": "inside agent change"})
			outsideArgs, _ := json.Marshal(map[string]string{"path": outsidePath, "content": "outside agent change"})
			_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_inside\",\"type\":\"function\",\"function\":{\"name\":\"write_file\",\"arguments\":%q}},{\"index\":1,\"id\":\"call_outside\",\"type\":\"function\",\"function\":{\"name\":\"write_file\",\"arguments\":%q}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n", string(insideArgs), string(outsideArgs))
			return
		}
		if err := os.WriteFile(unrelatedPath, []byte("unrelated external change"), 0o644); err != nil {
			t.Errorf("write unrelated external file: %v", err)
			return
		}
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	ws := NewWorkspace(project, "history-model", "tester", ttypes.Settings{}, &testLogger{})
	t.Cleanup(ws.Close)
	ws.SetAppDefaults(ttypes.AppSettings{LocalHistory: ttypes.LocalHistorySettings{Enabled: "on", UseGitIgnore: "on", CaptureExternalChanges: "on", SafetySnapshotBeforeRestore: "on", MaxFileSizeMB: 10}})
	frontend, err := ws.CreateSession(historyE2EConfig(server.URL), home)
	if err != nil {
		t.Fatal(err)
	}
	frontend.backend.SetYolo(true)
	sender := &historyE2ESender{events: make(chan historyE2EEvent, 64)}
	orchestrator, err := ws.StartOrchestrator(sender)
	if err != nil {
		t.Fatal(err)
	}
	if !frontend.backend.SubmitInput(orchestrator.ID, provider.PlainInput("write inside and outside")) {
		t.Fatal("turn was not admitted")
	}
	waitHistoryAgentDone(t, sender, orchestrator.ID)
	scopes := waitAgentHistoryScopes(t, ws.HistoryService, frontend.SessionID, orchestrator.ID)
	plan, err := ws.HistoryService.PlanAgentRestore(context.Background(), frontend.SessionID, scopes[0].MessageID, orchestrator.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantOutside := filepath.ToSlash(filepath.Clean(outsidePath))
	wantPaths := map[string]bool{"inside.txt": true, wantOutside: true}
	if len(plan.Changes) != 2 {
		t.Fatalf("agent plan=%+v", plan.Changes)
	}
	choices := make([]localhistory.RestoreChoice, 0, len(plan.Changes))
	for _, change := range plan.Changes {
		if !wantPaths[change.Path] {
			t.Fatalf("unowned path entered plan: %+v", change)
		}
		choices = append(choices, localhistory.RestoreChoice{Path: change.Path})
	}
	result, err := ws.ApplyHistoryRestoreChoices(context.Background(), plan.ID, choices)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{insidePath, outsidePath} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("agent path remained after undo: %s err=%v", path, err)
		}
	}
	if data, err := os.ReadFile(unrelatedPath); err != nil || string(data) != "unrelated external change" {
		t.Fatalf("unrelated external file=%q err=%v", data, err)
	}
	safetyPlan, err := ws.LocalHistory.PlanRestore(context.Background(), result.SafetySnapshotID, localhistory.SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var safetyChoices []localhistory.RestoreChoice
	for _, change := range safetyPlan.Changes {
		if wantPaths[change.Path] {
			safetyChoices = append(safetyChoices, localhistory.RestoreChoice{Path: change.Path})
		}
	}
	if len(safetyChoices) != 2 {
		t.Fatalf("safety inverse omitted external path: %+v", safetyPlan.Changes)
	}
	if _, err := ws.ApplyHistoryRestoreChoices(context.Background(), safetyPlan.ID, safetyChoices); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(insidePath); err != nil || string(data) != "inside agent change" {
		t.Fatalf("inside safety restore=%q err=%v", data, err)
	}
	if data, err := os.ReadFile(outsidePath); err != nil || string(data) != "outside agent change" {
		t.Fatalf("outside safety restore=%q err=%v", data, err)
	}
}
