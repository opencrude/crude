package app

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"crude/collaboration"
	"crude/core"
	"crude/coreclient"
	"crude/coreprotocol"
	"crude/provider"
	"crude/session"
	"crude/sessiondb"
	"crude/types"
	"crude/workspace"
)

// This opt-in journey uses the configured connection only for the observer. The
// inspected agent is cold saved evidence and must never be opened or executed.
func TestLiveAgentInspectionIsPassiveAndSelfExplanatory(t *testing.T) {
	if os.Getenv("CRUDE_LIVE_AGENT_INSPECTION") != "1" {
		t.Skip("set CRUDE_LIVE_AGENT_INSPECTION=1")
	}
	fc, err := loadFileConfigFrom(os.Getenv("CRUDE_INSPECTION_CONFIG"))
	if err != nil {
		t.Fatal("configured live account unavailable")
	}
	cfg := FileConfigToSession(fc)
	profile := cfg.Agents.Profiles[cfg.Agents.ActiveProfile]
	main := cfg.GetActiveDefaultCategory()
	if model := os.Getenv("CRUDE_INSPECTION_MODEL"); model != "" {
		main.Model = model
	}
	main.ThinkingEffort, main.Policy = "low", "default"
	main.AllowedTools = []string{"agent_list", "agent_inspect", "artifact_read", "board_read", "search_history", "read_file", "grep_files", "execute_command", "crude_commands", "crude_control", "reply"}
	main.DeniedTools = nil
	profile.Categories[session.CategoryDefault] = main
	cfg.Agents.Profiles[cfg.Agents.ActiveProfile] = profile
	cfg.Memory.AutoRecall, cfg.Memory.AutoExtract, cfg.Memory.Consolidation = "off", "off", "off"
	fc.Agents, fc.Memory = &cfg.Agents, &cfg.Memory
	home, project := t.TempDir(), t.TempDir()
	t.Setenv("CRUDE_HOME", home)
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
	client, err := coreclient.Dial(t.Context(), coreclient.SingleCarrier(wire), "inspection-live")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	done := make(chan string, 8)
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
	defer cancel()
	var attached coreprotocol.AttachResult
	if err := client.Call(ctx, "", coreprotocol.Attach, coreprotocol.AttachRequest{Root: project}, &attached); err != nil {
		t.Fatal(err)
	}
	followLiveAgentCompletions(t, client, attached.WorkspaceID, done)
	store := ws.Collaboration
	peer, err := store.RegisterAgent(ctx, collaboration.AgentRegistration{ID: "mara", Name: "Mara", SessionID: "mara-session", OwnerInstance: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	notice, err := store.Publish(ctx, collaboration.PublishRequest{SenderID: peer.ID, RecipientIDs: []string{peer.ID}, Origin: collaboration.OriginRuntime, Kind: collaboration.EventPeerNote, IdempotencyKey: "child-cancelled", Body: "Your old child was cancelled during janitorial cleanup; its obsolete task will not continue."})
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.StartRun(ctx, collaboration.StartRunRequest{AgentID: peer.ID, RuntimeEpoch: peer.RuntimeEpoch, Origin: collaboration.OriginPeer})
	if err != nil {
		t.Fatal(err)
	}
	peerPath := filepath.Join(ws.WorkspaceDir, "sessions", peer.SessionID+".db")
	if err := os.MkdirAll(filepath.Dir(peerPath), 0700); err != nil {
		t.Fatal(err)
	}
	db, err := sessiondb.OpenAndMigrate(peerPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := sessiondb.CreateSessionRow(db, peer.SessionID); err != nil {
		t.Fatal(err)
	}
	input := provider.NewTextMessage("user", notice.Event.Body)
	input.InternalID, input.Origin = notice.Event.ID, provider.OriginPeer
	answer := provider.NewTextMessage("assistant", "Inspected the cancelled child. No files were edited and no coding task was resumed. Verification code: INSPECTION_MARA_7Q.")
	answer.InternalID, answer.ReasoningContent = "mara-final", "PRIVATE_REASONING_NEVER_VISIBLE"
	stamp := time.Now().UnixNano()
	if _, err := sessiondb.PublishContext(db, sessiondb.ContextPublication{SessionID: peer.SessionID, AgentID: peer.SessionID, RuntimeAgentID: peer.ID, Mutation: sessiondb.ContextMutation{Append: []sessiondb.ContextMessageDelta{
		{Message: input, Turn: 9, AgentID: peer.ID, CreatedAt: stamp, Meta: sessiondb.DisplayMeta{TurnGeneration: 9, Snapshots: []types.ConversationMessage{{Role: "user", Content: notice.Event.Body}}}},
		{Message: answer, Turn: 9, AgentID: peer.ID, CreatedAt: stamp, Meta: sessiondb.DisplayMeta{TurnGeneration: 9, Snapshots: []types.ConversationMessage{{Role: "thinking", Content: answer.ReasoningContent}, {Role: "assistant", Content: answer.ContentString()}}}},
	}}}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.FinishRun(ctx, collaboration.FinishRunRequest{RunID: run.ID, AgentID: peer.ID, RuntimeEpoch: peer.RuntimeEpoch, ExpectedVersion: run.Version, State: collaboration.RunSucceeded, Outcome: "completed", ContextCommitID: notice.Event.ID}); err != nil {
		t.Fatal(err)
	}
	if err := store.DetachAgent(ctx, peer.ID, "fixture", peer.RuntimeEpoch); err != nil {
		t.Fatal(err)
	}
	before, err := store.Agent(ctx, peer.ID)
	if err != nil {
		t.Fatal(err)
	}
	beforeBytes, err := os.ReadFile(peerPath)
	if err != nil {
		t.Fatal(err)
	}
	var created workspace.NewSessionResult
	if err := client.Call(ctx, attached.WorkspaceID, coreprotocol.NewSession, &coreprotocol.InvocationSessionRequest{}, &created); err != nil || created.Error != "" {
		t.Fatal(err, created.Error)
	}
	ws.BackendSession(created.SessionID).SetYolo(true)
	goal := "Why was Mara working earlier? Is she still working, what woke her, and what did she actually do? Include any verification code in her latest visible answer. Inspect only: do not message, open, restart or control her session, and do not change files."
	var admission coreprotocol.InputResult
	if err := client.Call(ctx, attached.WorkspaceID, coreprotocol.SubmitInput, &coreprotocol.InputRequest{AgentID: created.OrchID, Text: goal, DraftRevision: 1}, &admission); err != nil {
		t.Fatal(err)
	}
	if admission.Status != session.InputAdmissionAccepted || admission.Error != "" {
		t.Fatalf("inspection input was not admitted: %s %s", admission.Status, admission.Error)
	}
	for finished := false; !finished; {
		select {
		case id := <-done:
			finished = id == created.OrchID
		case <-ctx.Done():
			diagnostic, stop := context.WithTimeout(context.Background(), time.Second)
			defer stop()
			var snapshot coreprotocol.WorkspaceSnapshot
			if err := client.Call(diagnostic, attached.WorkspaceID, coreprotocol.Snapshot, &coreprotocol.Empty{}, &snapshot); err == nil {
				for _, agent := range snapshot.Agents {
					if agent.ID == created.OrchID {
						t.Logf("observer model=%s running=%v completed=%v status=%s recovery=%+v", agent.Model, agent.Running, agent.Completed, agent.Status, agent.Recovery)
					}
				}
			}
			t.Fatal("inspection did not complete", ctx.Err())
		}
	}
	backend := ws.BackendSession(created.SessionID)
	used := map[string]int{}
	for _, row := range backend.MessagesWithMeta() {
		for _, call := range row.Msg.ToolCalls {
			used[call.Function.Name]++
		}
	}
	if used["agent_list"] == 0 || used["agent_inspect"] == 0 || used["execute_command"] != 0 || used["crude_control"] != 0 || used["agent_send"] != 0 {
		t.Error("inspection bypassed supported passive evidence", used)
	}
	final := liveLastAnswer(backend)
	if !strings.Contains(final, "INSPECTION_MARA_7Q") || strings.Contains(final, "PRIVATE_REASONING") {
		t.Error("latest public work not correctly reported", final)
	}
	after, err := store.Agent(ctx, peer.ID)
	afterBytes, readErr := os.ReadFile(peerPath)
	if err != nil || readErr != nil || !reflect.DeepEqual(before, after) || string(beforeBytes) != string(afterBytes) || ws.BackendSession(peer.SessionID) != nil {
		t.Fatal("inspection changed or activated the cold agent", err, readErr)
	}
	var deliveryState string
	if err := ws.WorkspaceDB.DB.QueryRow(`SELECT state FROM collaboration_deliveries WHERE id=?`, notice.Deliveries[0].ID).Scan(&deliveryState); err != nil || deliveryState != "pending" {
		t.Fatal("inspection consumed the target's delivery", err, deliveryState)
	}
	encoded, _ := json.Marshal(used)
	t.Logf("LIVE_INSPECTION_PASS model=%s tools=%s answer=%s", main.Model, encoded, final)
}

// These observers consume the same acknowledged workspace log as a shell; a
// controller that does not subscribe receives no duplicate invalidations.
func followLiveAgentCompletions(t *testing.T, client *coreclient.Client, ws string, done chan<- string) {
	t.Helper()
	sub := client.Subscribe(t.Context(), ws, coreprotocol.TopicWorkspace, "", func(_ coreprotocol.TopicHeader, body []byte) error {
		var update coreprotocol.WorkspaceUpdate
		if err := coreprotocol.Decode(body, &update); err != nil {
			return err
		}
		for _, reduction := range update.Reductions {
			if reduction.Effects.Done != nil {
				select {
				case done <- reduction.AgentID:
				case <-t.Context().Done():
					return t.Context().Err()
				}
			}
		}
		return nil
	})
	t.Cleanup(sub.Close)
}
