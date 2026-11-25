package core

import (
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"crude/agent/provider"
	"crude/agent/session"
	"crude/agent/sessiondb"
	"crude/core/coreprotocol"
	"crude/internal/providersim"
	"crude/types/conversation"
	"crude/types/setting"
	"crude/workspace"
)

// compactionAuthorityFixture is a real core with one session whose provider
// requests are scripted per kind: model turns (tool use allowed) or
// checkpoint extractions (tool_choice none). Each model turn is held until
// the test releases it, so /compact can arrive while the turn is active.
type compactionAuthorityFixture struct {
	t        *testing.T
	c        *Connection
	ws       *workspace.Workspace
	wsID     string
	created  workspace.NewSessionResult
	mu       sync.Mutex
	kinds    []string
	turnHeld chan *providersim.Gate
}

func newCompactionAuthorityFixture(t *testing.T, turns []string) *compactionAuthorityFixture {
	t.Helper()
	home := t.TempDir()
	setCoreTestHome(t, home)
	t.Setenv("USERPROFILE", home)
	f := &compactionAuthorityFixture{t: t, turnHeld: make(chan *providersim.Gate, 8)}
	var turn int
	server := providersim.New(t)
	server.Respond(func(request providersim.Request) providersim.Turn {
		if string(request.ToolChoice) == `"none"` {
			f.record("extraction")
			return providersim.Text("<<<intent>>>\nRetain the human request.\n<<<work>>>\nEvidence retained.\n<<<state>>>\nClean.\n<<<continuation>>>\nContinue the current step.")
		}
		f.mu.Lock()
		script := "DONE"
		if turn < len(turns) {
			script = turns[turn]
		}
		turn++
		f.mu.Unlock()
		f.record("turn")
		gate := providersim.NewGate()
		f.turnHeld <- gate
		if script == "read" {
			return providersim.CallTools(providersim.ToolCall{ID: "compact-read", Name: "read_file", Arguments: `{"path":"missing.txt"}`}).WithGate(gate)
		}
		return providersim.Text(script).WithGate(gate)
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
		f.ws = w
		return w, nil
	}, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })

	c, err := runtime.Connect("compaction-authority")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	f.c = c
	var attached coreprotocol.AttachResult
	if err := coreprotocol.Decode(callCore(t, c, "attach", "", coreprotocol.Attach, &coreprotocol.AttachRequest{Root: t.TempDir()}).Payload, &attached); err != nil {
		t.Fatal(err)
	}
	f.wsID = attached.WorkspaceID
	if err := coreprotocol.Decode(callCore(t, c, "create", f.wsID, coreprotocol.NewSession, coreprotocol.InvocationSessionRequest{}).Payload, &f.created); err != nil {
		t.Fatal(err)
	}
	f.ws.BackendSession(f.created.SessionID).SetYolo(true)
	return f
}

func (f *compactionAuthorityFixture) record(kind string) {
	f.mu.Lock()
	f.kinds = append(f.kinds, kind)
	f.mu.Unlock()
}

func (f *compactionAuthorityFixture) requestKinds() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.kinds, ",")
}

func (f *compactionAuthorityFixture) compact(id string) conversation.CompactResultMsg {
	f.t.Helper()
	var result conversation.CompactResultMsg
	if err := coreprotocol.Decode(callCore(f.t, f.c, id, f.wsID, coreprotocol.AgentCompact, coreprotocol.AgentIDRequest{AgentID: f.created.OrchID}).Payload, &result); err != nil {
		f.t.Fatal(err)
	}
	return result
}

func (f *compactionAuthorityFixture) awaitTurnRequest() *providersim.Gate {
	f.t.Helper()
	select {
	case gate := <-f.turnHeld:
		return gate
	case <-time.After(10 * time.Second):
		f.t.Fatalf("model request did not start; requests=%s", f.requestKinds())
	}
	return nil
}

// awaitRecord waits for the durable outcome of one exact compaction.
func (f *compactionAuthorityFixture) awaitRecord(operationID string) sessiondb.CompactionRecord {
	f.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		for _, record := range f.ws.BackendSession(f.created.SessionID).CompactionRecords(f.created.OrchID) {
			if record.OperationID == operationID && sessiondb.IsCompactionTerminal(record.State) {
				return record
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	f.t.Fatalf("compaction %s did not settle; requests=%s", operationID, f.requestKinds())
	return sessiondb.CompactionRecord{}
}

func (f *compactionAuthorityFixture) awaitIdle() {
	f.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var view coreprotocol.WorkspaceSnapshot
		if err := coreprotocol.Decode(callCore(f.t, f.c, "snapshot", f.wsID, coreprotocol.Snapshot, coreprotocol.Empty{}).Payload, &view); err != nil {
			f.t.Fatal(err)
		}
		if len(view.Agents) > 0 && !view.Agents[0].Running && view.Agents[0].ActiveCompaction.OperationID == "" {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	f.t.Fatal("agent did not become idle")
}

// A /compact typed while the agent works is accepted, not refused: the run
// compacts before its next model request and then continues the same turn.
func TestCoreCompactDuringActiveTurnRunsBeforeNextRequest(t *testing.T) {
	f := newCompactionAuthorityFixture(t, []string{"read", "Finished after compaction."})
	callCore(t, f.c, "input", f.wsID, coreprotocol.SubmitInput, coreprotocol.InputRequest{AgentID: f.created.OrchID, Text: "Keep this request.\n" + strings.Repeat("retained source ", 4_000)})
	gate := f.awaitTurnRequest()

	result := f.compact("compact-active")
	if result.Error != "" || result.Cancelled || result.OperationID == "" || result.Desc != "Compaction will start after the current step." {
		t.Fatalf("active-turn /compact result=%+v", result)
	}
	if again := f.compact("compact-again"); again.Error != "" || again.OperationID != result.OperationID || !strings.Contains(again.Desc, "already requested") {
		t.Fatalf("repeated /compact result=%+v", again)
	}
	gate.Release()
	gate = f.awaitTurnRequest()
	if got := f.requestKinds(); got != "turn,extraction,turn" {
		t.Fatalf("provider requests=%s; want the checkpoint before the next model request", got)
	}
	gate.Release()
	record := f.awaitRecord(result.OperationID)
	if record.State != sessiondb.CompactionCompleted || record.Trigger != "requested" {
		t.Fatalf("requested compaction record=%+v", record)
	}
	f.awaitIdle()
}

// A turn that finishes before reaching another step hands the accepted
// request to an exclusive compaction instead of dropping it.
func TestCoreCompactAcceptedNearTurnEndRunsAfterTheTurn(t *testing.T) {
	f := newCompactionAuthorityFixture(t, []string{"Final answer."})
	callCore(t, f.c, "input", f.wsID, coreprotocol.SubmitInput, coreprotocol.InputRequest{AgentID: f.created.OrchID, Text: "Keep this request.\n" + strings.Repeat("retained source ", 4_000)})
	gate := f.awaitTurnRequest()

	result := f.compact("compact-final")
	if result.Error != "" || result.OperationID == "" {
		t.Fatalf("active-turn /compact result=%+v", result)
	}
	gate.Release()
	record := f.awaitRecord(result.OperationID)
	if record.State != sessiondb.CompactionCompleted || record.Trigger != "manual" {
		t.Fatalf("turn-end compaction record=%+v requests=%s", record, f.requestKinds())
	}
	if got := f.requestKinds(); got != "turn,extraction" {
		t.Fatalf("provider requests=%s", got)
	}
	f.awaitIdle()
}

// Stop settles an accepted request visibly; it never silently disappears.
func TestCoreStopSettlesAcceptedCompactionRequest(t *testing.T) {
	f := newCompactionAuthorityFixture(t, []string{"read"})
	callCore(t, f.c, "input", f.wsID, coreprotocol.SubmitInput, coreprotocol.InputRequest{AgentID: f.created.OrchID, Text: "Keep this request."})
	gate := f.awaitTurnRequest()
	defer gate.Release()
	result := f.compact("compact-before-stop")
	if result.OperationID == "" {
		t.Fatalf("active-turn /compact result=%+v", result)
	}
	var view coreprotocol.WorkspaceSnapshot
	if err := coreprotocol.Decode(callCore(t, f.c, "snapshot", f.wsID, coreprotocol.Snapshot, coreprotocol.Empty{}).Payload, &view); err != nil {
		t.Fatal(err)
	}
	callCore(t, f.c, "stop", f.wsID, coreprotocol.CancelAgent, coreprotocol.AgentRequest{AgentID: f.created.OrchID, Generation: view.Agents[0].TurnIdentity.Generation})
	record := f.awaitRecord(result.OperationID)
	if record.State != sessiondb.CompactionCancelled {
		t.Fatalf("stopped request record=%+v", record)
	}
	if got := f.requestKinds(); strings.Contains(got, "extraction") {
		t.Fatalf("Stop still compacted: requests=%s", got)
	}
}
