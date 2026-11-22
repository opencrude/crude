package workspace

import (
	"context"
	"crude/types"
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

	"crude/agent/provider"
	"crude/workspace/localhistory"
)

func TestSessionLifecycleWorkerDoesNotPublishCollections(t *testing.T) {
	setTestHome(t)
	ws := NewWorkspace(t.TempDir(), "history-model", "tester", types.Settings{}, nopLogger{})
	t.Cleanup(ws.Close)
	ws.SetAppDefaults(types.AppSettings{LocalHistory: types.LocalHistorySettings{Enabled: "off"}})
	cfg := historyE2EConfig("http://127.0.0.1:1")
	ws.SetBackendConfig(cfg, t.TempDir())
	ws.SetEventSender(&historyE2ESender{events: make(chan historyE2EEvent, 1024)})
	op, err := ws.PrepareAgentSession("")
	if err != nil {
		t.Fatal(err)
	}
	// Mutating configuration after preparation cannot affect the backend work.
	profile := cfg.Agents.Profiles[cfg.Agents.ActiveProfile]
	category := profile.Categories["default"]
	category.Model = "changed-after-prepare"
	profile.Categories["default"] = category
	result := op.Run(context.Background())
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	if len(ws.Sessions) != 0 || len(ws.SessionsByID) != 0 || ws.AgentMgr.Count() != 0 {
		t.Fatal("worker published workspace state")
	}
	if result.owner.Settings.Effective.Model == "changed-after-prepare" {
		t.Fatal("worker borrowed mutable config")
	}
	first := ws.ApplyAgentSession(result)
	if first.Error != "" {
		t.Fatal(first.Error)
	}
	second := ws.ApplyAgentSession(op.Run(context.Background()))
	if second.Error != "" || second.OrchID != first.OrchID || len(ws.Sessions) != 1 {
		t.Fatal("repeat application duplicated backend")
	}
	op, err = ws.PrepareAgentSession(first.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	warm := ws.ApplyAgentSession(op.Run(context.Background()))
	if warm.Error != "" || warm.OrchID != first.OrchID {
		t.Fatalf("warm open: %+v", warm)
	}
}

func TestSessionLifecycleCancellationDoesNotInstall(t *testing.T) {
	setTestHome(t)
	ws := NewWorkspace(t.TempDir(), "history-model", "tester", types.Settings{}, nopLogger{})
	t.Cleanup(ws.Close)
	ws.SetBackendConfig(historyE2EConfig("http://127.0.0.1:1"), t.TempDir())
	ws.SetEventSender(&historyE2ESender{events: make(chan historyE2EEvent, 1024)})
	op, err := ws.PrepareAgentSession("")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result := ws.ApplyAgentSession(op.Run(ctx))
	if result.Error == "" || len(ws.Sessions) != 0 {
		t.Fatal("cancelled lifecycle installed")
	}
}

func TestLifecycleEventGateInstallsEarlyEvidence(t *testing.T) {
	ws := &Workspace{Logger: nopLogger{}, AgentMgr: NewAgentManager(), PaneMgr: NewPaneManager(), SessionsByAgent: map[string]*Session{}}
	node := ws.RegisterAgentRoot("root")
	gate := &lifecycleEventGate{sender: &historyE2ESender{events: make(chan historyE2EEvent, 10)}}
	gate.SendAgentEventWithID("stream_token", "early retained output", "root")
	if len(node.State.Messages.Messages()) != 0 {
		t.Fatal("early evidence mutated inventory before application")
	}
	gate.activate(ws)
	if len(node.State.Messages.Messages()) == 0 {
		t.Fatal("startup evidence dropped")
	}
}

type historyWarningLogger struct {
	nopLogger
	mu       sync.Mutex
	warnings []string
}

func (l *historyWarningLogger) Warn(tag, msg string, pairs ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.warnings = append(l.warnings, tag+" "+msg)
}

func (l *historyWarningLogger) historyWarnings() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []string
	for _, warning := range l.warnings {
		if strings.HasPrefix(warning, "HISTORY ") {
			out = append(out, warning)
		}
	}
	return out
}

func historyLifecycleWorkspace(t *testing.T, root string, logger Logger) *Workspace {
	t.Helper()
	ws := NewWorkspace(root, "history-model", "tester", types.Settings{}, logger)
	t.Cleanup(ws.Close)
	ws.SetAppDefaults(types.AppSettings{LocalHistory: types.LocalHistorySettings{Enabled: "on", MaxFileSizeMB: 10}})
	if ws.HistoryService == nil || ws.LocalHistory == nil {
		t.Fatal("Local History did not start")
	}
	ws.SetBackendConfig(historyE2EConfig("http://127.0.0.1:1"), t.TempDir())
	ws.SetEventSender(&historyE2ESender{events: make(chan historyE2EEvent, 1024)})
	return ws
}

// The workspace actor applies session lifecycles. Local History retention
// held the history store's only connection for 8 s while a new agent was
// created, and every request waited behind the actor. Applying a created
// session must not query that store. A closed store stands in for a busy one:
// a query from the installing owner fails loudly instead of waiting.
func TestSessionLifecycleApplyDoesNotQueryLocalHistory(t *testing.T) {
	setTestHome(t)
	logger := &historyWarningLogger{}
	ws := historyLifecycleWorkspace(t, t.TempDir(), logger)
	op, err := ws.PrepareAgentSession("")
	if err != nil {
		t.Fatal(err)
	}
	result := op.Run(context.Background())
	if err := ws.LocalHistory.Close(); err != nil {
		t.Fatal(err)
	}
	created := ws.ApplyAgentSession(result)
	if created.Error != "" {
		t.Fatal(created.Error)
	}
	if warnings := logger.historyWarnings(); len(warnings) != 0 {
		t.Fatalf("creating a session queried Local History on the installing owner: %q", warnings)
	}
}

// A restore whose notification was not delivered still reaches its session
// when the session is opened again. The lifecycle worker reads it; the
// installing owner only installs it.
func TestResumedSessionReceivesPendingRestoreNotification(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	project := t.TempDir()
	path := filepath.Join(project, "notes.txt")
	if err := os.WriteFile(path, []byte("a=0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if requests.Add(1) == 1 {
			arguments, _ := json.Marshal(map[string]string{"path": path, "old_string": "a=0", "new_string": "a=agent"})
			_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_edit\",\"type\":\"function\",\"function\":{\"name\":\"edit_file\",\"arguments\":%q}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n", string(arguments))
			return
		}
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"edited\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	cfg := historyE2EConfig(server.URL)
	ws := NewWorkspace(project, "history-model", "tester", types.Settings{}, &testLogger{})
	t.Cleanup(ws.Close)
	ws.SetAppDefaults(types.AppSettings{LocalHistory: types.LocalHistorySettings{Enabled: "on", UseGitIgnore: "on", SafetySnapshotBeforeRestore: "on", MaxFileSizeMB: 10}})
	frontend, err := ws.CreateSession(cfg, home)
	if err != nil {
		t.Fatal(err)
	}
	frontend.backend.SetYolo(true)
	sender := &historyE2ESender{events: make(chan historyE2EEvent, 1024)}
	orchestrator, err := ws.StartOrchestrator(frontend.SessionID, sender)
	if err != nil {
		t.Fatal(err)
	}
	if !frontend.backend.SubmitInput(orchestrator.ID, provider.PlainInput("edit a")) {
		t.Fatal("turn was not admitted")
	}
	waitHistoryAgentDone(t, sender, orchestrator.ID)
	scopes := waitAgentHistoryScopes(t, ws.HistoryService, frontend.SessionID, orchestrator.ID)
	plan, err := ws.HistoryService.PlanAgentRestore(context.Background(), frontend.SessionID, scopes[0].MessageID, orchestrator.ID)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := ws.ApplyHistoryRestoreChoices(context.Background(), plan.ID, []localhistory.RestoreChoice{{Path: "notes.txt"}})
	if err != nil {
		t.Fatal(err)
	}
	// The restore committed, but its session closed before the notice.
	sessionID := frontend.SessionID
	ws.CloseAllSessions()
	if pending, err := ws.HistoryService.PendingRestoreNotifications(sessionID); err != nil || len(pending) != 1 {
		t.Fatalf("pending notifications = %+v, %v; want the undelivered restore", pending, err)
	}

	ws.SetBackendConfig(cfg, home)
	ws.SetEventSender(sender)
	op, err := ws.PrepareAgentSession(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	result := op.Run(context.Background())
	if len(result.pendingRestores) != 1 || result.pendingRestores[0].PlanID != restored.PlanID {
		t.Fatalf("worker did not read the pending restore: %+v, %v", result.pendingRestores, result.pendingRestoresErr)
	}
	opened := ws.ApplyAgentSession(result)
	if opened.Error != "" {
		t.Fatal(opened.Error)
	}
	if pending, err := ws.HistoryService.PendingRestoreNotifications(sessionID); err != nil || len(pending) != 0 {
		t.Fatalf("restore notification was not delivered: %+v, %v", pending, err)
	}
	delivered := false
	for _, row := range ws.SessionsByID[sessionID].backend.MessagesWithMeta() {
		for _, snapshot := range row.Meta.Snapshots {
			if snapshot.LifecyclePhase == "history_restored" && snapshot.LifecycleOperationID == restored.PlanID {
				delivered = true
			}
		}
	}
	if !delivered {
		t.Fatal("the reopened session has no restore notice")
	}
}
