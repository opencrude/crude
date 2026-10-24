package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/agent"
	"crude/collaboration"
	"crude/core"
	"crude/coreclient"
	"crude/coreprotocol"
	"crude/history"
	"crude/provider"
	"crude/session"
	"crude/sessiondb"
	"crude/shellport/remote"
	"crude/tui"
	"crude/tui/viewmodel"
	"crude/types"
	recoverytypes "crude/types/recovery"
	"crude/workspace"
	"crude/worktree"
)

// Opt-in acceptance uses the user's explicitly selected configured connection in
// memory. It never persists credentials, touches the real workspace or starts its
// retained core. Run models separately for independent, inspectable evidence.
func TestLiveExecutionHygieneAutonomy(t *testing.T) {
	if os.Getenv("CRUDE_LIVE_EXECUTION_HYGIENE") != "1" {
		t.Skip("set CRUDE_LIVE_EXECUTION_HYGIENE=1")
	}
	fc := LoadFileConfig()
	if path := os.Getenv("CRUDE_HYGIENE_CONFIG"); path != "" {
		var err error
		fc, err = loadFileConfigFrom(path)
		if err != nil {
			t.Fatal("cannot read configured live account")
		}
	}
	cfg := FileConfigToSession(fc)
	main := cfg.GetActiveDefaultCategory()
	model := strings.TrimSpace(os.Getenv("CRUDE_HYGIENE_MODEL"))
	if model == "" {
		model = main.Model
	}
	if main.AccountID == "" || model == "" {
		t.Fatal("configured account/model required")
	}
	home := t.TempDir()
	t.Setenv("CRUDE_HOME", home)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	project := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", project}, args...)...).CombinedOutput(); err != nil {
			t.Fatal(err, string(out))
		}
	}
	git("init", "-q")
	for name, text := range map[string]string{"manual.txt": "user deliberate baseline\n", "shared.txt": "base\n", ".gitignore": "build-cache\n"} {
		if err := os.WriteFile(filepath.Join(project, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	git("add", ".")
	git("-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "base")
	profile := cfg.Agents.Profiles[cfg.Agents.ActiveProfile]
	on, off := true, false
	profile.MultiAgent = &on
	profile.TurnLogs = &off
	main.Model, main.Policy, main.ThinkingEffort = model, "default", "low"
	// No manual shell/Git escape: the model must use the harness execution
	// commands for all janitorial effects and native files for its actual task.
	main.AllowedTools = []string{"crude_commands", "crude_control", "agent_list", "agent_inspect", "agent_send", "agent_start", "agent_wait", "agent_control", "artifact_read", "read_file", "write_file", "reply", "update_plan", "get_plan"}
	main.DeniedTools = nil
	profile.Categories[session.CategoryDefault] = main
	reader := main
	reader.Policy = "read-only"
	reader.AllowedTools = []string{"read_file", "publish_report", "agent_send"}
	reader.DeniedTools = nil
	profile.Categories[session.CategoryResearcher] = reader
	cfg.Agents.Profiles[cfg.Agents.ActiveProfile] = profile
	cfg.Agents.Enabled = true
	one := 1
	cfg.Agents.ExecutionHygiene = types.ExecutionHygieneSettings{RetainedCount: &one}
	cfg.Memory.AutoRecall, cfg.Memory.AutoExtract, cfg.Memory.Consolidation = "off", "off", "off"
	fc.Agents = &cfg.Agents
	fc.Memory = &cfg.Memory
	application, err := New(Opts{FC: fc, Home: home, ProjectDir: project})
	if err != nil {
		t.Fatal(err)
	}
	var ws *workspace.Workspace
	runtime, err := core.New(func(root string) (*workspace.Workspace, error) {
		var err error
		ws, err = application.newWorkspace(root, &appLogger{})
		return ws, err
	}, filepath.Join(home, "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	server, wire := net.Pipe()
	go func() { _ = core.ServeConnection(t.Context(), runtime, server) }()
	client, err := coreclient.Dial(t.Context(), coreclient.SingleCarrier(wire), "hygiene-live")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	doneAgents := make(chan string, 16)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()
	var attached coreprotocol.AttachResult
	if err := client.Call(ctx, "", coreprotocol.Attach, coreprotocol.AttachRequest{Root: project}, &attached); err != nil {
		t.Fatal(err)
	}
	followLiveAgentCompletions(t, client, attached.WorkspaceID, doneAgents)
	// Seed real terminal runs and managed checkouts before model admission.
	parent, err := ws.Collaboration.RegisterAgent(ctx, collaboration.AgentRegistration{ID: "prior-parent", Name: "Previous parent", SessionID: "prior-session", OwnerInstance: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	create := func(id string, state collaboration.RunState) worktree.Context {
		t.Helper()
		ag, err := ws.Collaboration.RegisterAgent(ctx, collaboration.AgentRegistration{ID: id, Name: id, SessionID: id, ParentID: parent.ID, OwnerInstance: "fixture"})
		if err != nil {
			t.Fatal(err)
		}
		run, err := ws.Collaboration.StartRun(ctx, collaboration.StartRunRequest{AgentID: ag.ID, RuntimeEpoch: ag.RuntimeEpoch, Origin: collaboration.OriginPeer})
		if err != nil {
			t.Fatal(err)
		}
		c, err := ws.Executions.Create(ctx, worktree.Request{Owner: worktree.Owner{AgentID: ag.ID, RunID: run.ID}, Mode: worktree.Isolated})
		if err != nil {
			t.Fatal(err)
		}
		if state != "" {
			if _, err := ws.Collaboration.FinishRun(ctx, collaboration.FinishRunRequest{RunID: run.ID, AgentID: ag.ID, RuntimeEpoch: ag.RuntimeEpoch, ExpectedVersion: run.Version, State: state, Outcome: "fixture settled"}); err != nil {
				t.Fatal(err)
			}
			if err := ws.Collaboration.DetachAgent(ctx, ag.ID, ag.OwnerInstance, ag.RuntimeEpoch); err != nil {
				t.Fatal(err)
			}
		}
		return c
	}
	clean := create("stale-clean-child", collaboration.RunSucceeded)
	failed := create("failed-conflicting-child", collaboration.RunFailed)
	kept := create("intentional-kept-child", collaboration.RunSucceeded)
	active := create("still-working-child", "")
	// A separate unopened saved parent has a genuinely recoverable child. This
	// is not ordinary stale-pin cleanup: the model must explicitly settle it.
	parked, parkedSession := seedLiveParkedExecution(t, ws, cfg, main.AccountID, model)
	if err := ws.Executions.Keep(kept.ID, kept.Owner); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(failed.Root, "shared.txt"), []byte("obsolete failed experiment\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(failed.Root, "build-cache"), []byte("discardable build bytes\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "shared.txt"), []byte("human intentional conflicting change\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "manual.txt"), []byte("human intentional unsaved-on-disk work\n"), 0600); err != nil {
		t.Fatal(err)
	}
	goal := fmt.Sprintf("Clean the abandoned managed execution leftovers in this workspace, then continue normal work. The prior parent's completed clean child and its failed obsolete experiment can be cleaned. The obsolete experiment should not be merged. The other cold parent's parked child experiment is unwanted too: explicitly settle its parked recovery through supported tools, preserve its history, then discard and clean its checkout; do not continue that obsolete task. Its owning root session is %s. Preserve the intentional Keep worktree and the still-running child's checkout. Preserve my manual.txt and conflicting shared.txt edits exactly. Use Crude's supported execution tools, not shell/manual Git cleanup. After cleanup, run one read-only researcher child to read manual.txt and report its first line, and write ready.txt containing exactly the 13 bytes HYGIENE_READY with no trailing newline. Verify those bytes before finishing. Do not ask me to do janitorial work that Crude can do. Explain any genuinely protected work briefly. Here is the compact starting inventory: clean=%s obsolete=%s parked=%s kept=%s running=%s.", parkedSession, clean.ID, failed.ID, parked.ID, kept.ID, active.ID)
	var created workspace.NewSessionResult
	if err := client.Call(ctx, attached.WorkspaceID, coreprotocol.NewSession, &coreprotocol.InvocationSessionRequest{}, &created); err != nil || created.Error != "" {
		t.Fatal(err, created.Error)
	}
	ws.BackendSession(created.SessionID).SetYolo(true)
	var terminal *tui.TUI
	if os.Getenv("CRUDE_VISUAL_HYGIENE") == "1" {
		dial := func(ctx context.Context, id string) (*coreclient.Client, error) {
			server, wire := net.Pipe()
			go func() { _ = core.ServeConnection(t.Context(), runtime, server) }()
			return coreclient.Dial(ctx, coreclient.SingleCarrier(wire), id)
		}
		port, err := remote.Attach(ctx, remote.Options{Root: project, Dial: dial})
		if err != nil {
			t.Fatal(err)
		}
		terminal, err = tui.Start(port, providerPresets(), history.New(), &appLogger{}, func(a *viewmodel.App) error { a.AddOrchestrator(created.OrchID); return nil })
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { terminal.Stop(); terminal.Wait() })
		port.StartReplica()
		time.Sleep(4 * time.Second)
		terminal.Program.Send(tea.PasteMsg{Content: goal})
		// Large paste is a real asynchronous reference store operation. Enter
		// before that operation settles intentionally keeps the draft unsent.
		time.Sleep(2 * time.Second)
		terminal.Program.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	} else {
		var admission coreprotocol.InputResult
		if err := client.Call(ctx, attached.WorkspaceID, coreprotocol.SubmitInput, &coreprotocol.InputRequest{AgentID: created.OrchID, Text: goal, DraftRevision: 1}, &admission); err != nil {
			t.Fatal(err)
		}
		if admission.Status != session.InputAdmissionAccepted || admission.Error != "" {
			t.Fatalf("hygiene input was not admitted: %s %s", admission.Status, admission.Error)
		}
	}
	var done bool
	for !done {
		select {
		case <-ctx.Done():
			t.Fatal("live hygiene model did not complete", model, ctx.Err())
		case agentID := <-doneAgents:
			done = agentID == created.OrchID
		}
	}
	toolsUsed := map[string]int{}
	controls := map[string]int{}
	for _, row := range ws.BackendSession(created.SessionID).MessagesWithMeta() {
		for _, call := range row.Msg.ToolCalls {
			toolsUsed[call.Function.Name]++
			if call.Function.Name == "crude_control" {
				var input struct {
					Command string `json:"command"`
				}
				_ = json.Unmarshal([]byte(call.Function.Arguments), &input)
				controls[input.Command]++
			}
		}
	}
	for _, c := range []worktree.Context{clean, failed, parked} {
		actual, err := ws.Executions.Inspect(c.ID)
		if err != nil || !actual.Removed {
			t.Errorf("model did not clean disposable execution %s: %v", c.ID, err)
		}
	}
	for _, c := range []worktree.Context{kept, active} {
		actual, err := ws.Executions.Inspect(c.ID)
		if err != nil || actual.Removed {
			t.Errorf("model removed protected work %s: %v", c.ID, err)
		}
	}
	for name, want := range map[string]string{"manual.txt": "human intentional unsaved-on-disk work\n", "shared.txt": "human intentional conflicting change\n", "ready.txt": "HYGIENE_READY"} {
		got, err := os.ReadFile(filepath.Join(project, name))
		if err != nil || string(got) != want {
			t.Errorf("live model final file %s=%q err=%v", name, got, err)
		}
	}
	if toolsUsed["agent_start"] == 0 || controls["execution.reconcile"] == 0 || controls["execution.cleanup"] < 3 || controls["execution.settle"] == 0 || controls["session.open"] == 0 {
		t.Error("model bypassed autonomous harness/child path", toolsUsed, controls)
	}
	t.Logf("LIVE_HYGIENE model=%s tools=%v controls=%v answer=%s", model, toolsUsed, controls, liveLastAnswer(ws.BackendSession(created.SessionID)))
	if !t.Failed() {
		if path := os.Getenv("CRUDE_HYGIENE_RESULT"); path != "" {
			if err := os.WriteFile(path, []byte("HYGIENE_SUCCESS "+model+" cold recovery settled; stale pins cleaned; conflict archived; human, Keep, running work preserved; researcher completed; work continued\n"), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if terminal != nil {
		hold, _ := strconv.Atoi(os.Getenv("CRUDE_HYGIENE_VISUAL_HOLD_SECONDS"))
		if hold > 0 {
			timer := time.NewTimer(time.Duration(hold) * time.Second)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
		}
	}
}

func seedLiveParkedExecution(t *testing.T, ws *workspace.Workspace, cfg *session.Config, account, model string) (worktree.Context, string) {
	t.Helper()
	const rootID, childID = "parked-session", "parked-child"
	ctx := t.Context()
	store := ws.Collaboration
	parent, err := store.RegisterAgent(ctx, collaboration.AgentRegistration{ID: rootID + "_orch_1", Name: "Parked parent", SessionID: rootID, OwnerInstance: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	child, err := store.RegisterAgent(ctx, collaboration.AgentRegistration{ID: childID, Name: "Parked child", SessionID: childID, ParentID: parent.ID, Category: session.CategoryResearcher, OwnerInstance: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.StartRun(ctx, collaboration.StartRunRequest{AgentID: childID, RuntimeEpoch: child.RuntimeEpoch, Origin: collaboration.OriginPeer})
	if err != nil {
		t.Fatal(err)
	}
	c, err := ws.Executions.Create(ctx, worktree.Request{Owner: worktree.Owner{AgentID: childID, RunID: run.ID}, Mode: worktree.Isolated})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.FinishRun(ctx, collaboration.FinishRunRequest{RunID: run.ID, AgentID: childID, RuntimeEpoch: child.RuntimeEpoch, ExpectedVersion: run.Version, State: collaboration.RunInterrupted, Outcome: "parked for attention"}); err != nil {
		t.Fatal(err)
	}
	for _, ag := range []collaboration.Agent{child, parent} {
		if err := store.DetachAgent(ctx, ag.ID, ag.OwnerInstance, ag.RuntimeEpoch); err != nil {
			t.Fatal(err)
		}
	}
	rootPath := filepath.Join(ws.WorkspaceDir, "sessions", rootID+".db")
	if err := os.MkdirAll(filepath.Join(ws.WorkspaceDir, "sessions", rootID, "subagents"), 0700); err != nil {
		t.Fatal(err)
	}
	parentDB, err := sessiondb.OpenAndMigrate(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	defer parentDB.Close()
	childDB, err := sessiondb.OpenAndMigrate(filepath.Join(ws.WorkspaceDir, "sessions", rootID, "subagents", childID+".db"))
	if err != nil {
		t.Fatal(err)
	}
	defer childDB.Close()
	providerID := cfg.Accounts[account].ProviderID
	if err := sessiondb.Create(parentDB, sessiondb.SessionMeta{ID: rootID, ProjectPath: ws.RootPath, ProjectName: filepath.Base(ws.RootPath), Model: model, Provider: providerID, AccountID: account, CreatedAt: time.Now().UnixNano()}); err != nil {
		t.Fatal(err)
	}
	if err := sessiondb.Create(childDB, sessiondb.SessionMeta{ID: childID, ProjectPath: c.Root, Model: model, Provider: providerID, AccountID: account, CreatedAt: time.Now().UnixNano(), ParentSessionID: sql.NullString{String: rootID, Valid: true}, AgentCategory: sql.NullString{String: session.CategoryResearcher, Valid: true}, AgentName: sql.NullString{String: child.Name, Valid: true}, Depth: 1}); err != nil {
		t.Fatal(err)
	}
	owner, _ := json.Marshal(struct {
		Spec agent.SubAgentSpec `json:"spec"`
	}{agent.SubAgentSpec{ID: childID, Name: child.Name, Category: session.CategoryResearcher, Description: "Obsolete parked experiment", ParentID: agent.AgentID(parent.ID), AccountID: account, Model: model, Provider: providerID, Isolation: "worktree", ExecutionID: c.ID, ExecutionRoot: c.Root, ExecutionRunID: run.ID, Depth: 1}})
	assignment := provider.NewTextMessage("user", "Obsolete parked experiment")
	assignment.InternalID = "assignment-parked-child"
	checkpoint := sessiondb.RecoveryCheckpoint{Status: recoverytypes.Status{ID: "parked-incident", AgentID: childID, Generation: 1, State: recoverytypes.NeedsAttention, StartedAt: time.Now(), UpdatedAt: time.Now(), Summary: "Parked obsolete experiment"}, PendingInput: provider.UserInput{InternalID: assignment.InternalID, Text: assignment.ContentString()}, InputAdmitted: true, Archive: []provider.ChatMessage{assignment}, ActiveContext: []provider.ChatMessage{assignment}, ContextOwnerID: childID, ContextGeneration: 1, ContextReason: "fixture_parked", OwnerData: owner}
	if err := sessiondb.SaveRecoveryCheckpoint(childDB, childID, checkpoint); err != nil {
		t.Fatal(err)
	}
	checkpoint.ContextOwnerID = ""
	if err := sessiondb.SaveRecoveryCheckpoint(parentDB, rootID, checkpoint); err != nil {
		t.Fatal(err)
	}
	if err := ws.WorkspaceDB.RegisterSession(rootID, ws.RootPath, filepath.Base(ws.RootPath), model, providerID); err != nil {
		t.Fatal(err)
	}
	return c, rootID
}
