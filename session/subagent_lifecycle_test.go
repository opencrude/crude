package session

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"crude/agent"
	"crude/provider"
	"crude/sessiondb"
)

func TestSubAgentCompletionWakeWaitsForQueueCapacity(t *testing.T) {
	wakeCh := make(chan WakeEvent, 1)
	wakeCh <- WakeEvent{Type: WakeSubAgentDone, SourceID: "existing"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runner := &SubAgentRunner{wakeCh: wakeCh, sessionCtx: ctx}
	delivered := make(chan bool, 1)
	go func() {
		delivered <- runner.deliverCompletionWake(agent.SubAgentResult{
			AgentID: "new-result", Status: agent.SubAgentCompleted,
		})
	}()

	select {
	case <-delivered:
		t.Fatal("completion wake was dropped instead of applying backpressure")
	case <-time.After(20 * time.Millisecond):
	}
	<-wakeCh
	select {
	case ok := <-delivered:
		if !ok {
			t.Fatal("completion wake was not delivered")
		}
	case <-time.After(time.Second):
		t.Fatal("completion wake did not unblock after queue capacity became available")
	}
	event := <-wakeCh
	if event.SourceID != "new-result" {
		t.Fatalf("wake event = %+v", event)
	}
}

func TestSubAgentTranscriptIsReplayable(t *testing.T) {
	db, err := sessiondb.OpenAndMigrate(filepath.Join(t.TempDir(), "child.db"))
	if err != nil {
		t.Fatalf("OpenAndMigrate: %v", err)
	}
	defer db.Close()
	if err := sessiondb.Create(db, sessiondb.SessionMeta{
		ID: "child", ProjectPath: "/project", ProjectName: "project",
		CreatedAt: time.Now().UnixNano(),
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	handle := &agent.SubAgentHandle{DB: db, SessionID: "child"}
	assistantTool := provider.ChatMessage{Role: "assistant", ToolCalls: []provider.ToolCall{{
		ID: "call-1", Function: provider.FunctionCall{Name: "execute_command", Arguments: `{"command":"go test ./..."}`},
	}}}
	toolResult := provider.NewTextMessage("tool", "tests passed")
	toolResult.ToolCallID = "call-1"
	if err := persistSubAgentTranscript(handle, []provider.ChatMessage{
		provider.NewTextMessage("system", "system"),
		provider.NewTextMessage("user", "inspect lifecycle"),
		assistantTool,
		toolResult,
		provider.NewTextMessage("assistant", "found the bug"),
	}); err != nil {
		t.Fatalf("persistSubAgentTranscript: %v", err)
	}
	messages, _, err := sessiondb.LoadMessages(db)
	if err != nil {
		t.Fatalf("LoadMessages: %v", err)
	}
	if len(messages) != 4 || messages[0].ContentString() != "inspect lifecycle" ||
		messages[3].ContentString() != "found the bug" {
		t.Fatalf("messages = %+v", messages)
	}
	withMeta, err := sessiondb.LoadMessagesWithMeta(db)
	if err != nil {
		t.Fatal(err)
	}
	if withMeta[0].Meta.Snapshots[0].Role != "assignment" || len(withMeta[2].Meta.Snapshots) != 1 ||
		withMeta[2].Meta.Snapshots[0].ToolName != "execute_command" ||
		withMeta[2].Meta.Snapshots[0].ToolCommand != "go test ./..." {
		t.Fatalf("canonical child transcript metadata = %+v", withMeta)
	}
}

func TestEverySubAgentTerminalPathPublishesEventResultAndWake(t *testing.T) {
	s := &Session{
		wakeCh:          make(chan WakeEvent, 1),
		consumedReports: make(map[string]bool),
	}
	runner := NewSubAgentRunner(s, 1)
	t.Cleanup(runner.SessionCancel)
	sender := newMockEventSender()
	runner.SetSender(sender)
	handle := &agent.SubAgentHandle{
		Spec: agent.SubAgentSpec{
			ID: "crashed-child", Name: "crashed child", Category: agent.CatResearcher,
		},
		ResultCh: make(chan agent.SubAgentResult, 1),
		Cancel:   func() {},
	}
	if err := runner.Manager().Register(handle); err != nil {
		t.Fatal(err)
	}
	result := agent.SubAgentResult{
		AgentID: handle.Spec.ID, Name: handle.Spec.Name, Category: handle.Spec.Category,
		Status: agent.SubAgentCrashed, Errors: []string{"panic"},
	}

	runner.finalizeSubAgent(handle, result)

	if got := <-handle.ResultCh; got.Status != agent.SubAgentCrashed {
		t.Fatalf("result channel status = %q", got.Status)
	}
	event := <-sender.ch
	if event.typ != "sub_agent_completed" || event.agentID != string(handle.Spec.ID) {
		t.Fatalf("terminal event = %#v", event)
	}
	wake := <-s.wakeCh
	got, ok := wake.Data.(agent.SubAgentResult)
	if wake.Type != WakeSubAgentDone || !ok || got.Status != agent.SubAgentCrashed {
		t.Fatalf("terminal wake = %#v", wake)
	}
	if _, ok := runner.Manager().GetActive(handle.Spec.ID); ok {
		t.Fatal("terminal child remained active")
	}
}

func TestCancelledSubAgentResultCannotReadAsSuccess(t *testing.T) {
	formatted := formatSubAgentResult(agent.SubAgentResult{
		Name: "child", Category: agent.CatResearcher, Status: agent.SubAgentCancelled,
	})
	if !strings.Contains(formatted, "CANCELLED") || !strings.Contains(formatted, "did not complete") || strings.Contains(formatted, "Status:** OK") {
		t.Fatalf("cancelled result = %q", formatted)
	}
}

func TestSubAgentDatabaseIsScopedUnderParentSession(t *testing.T) {
	home := t.TempDir()
	sessionsRoot := filepath.Join(home, "workspace-sessions")
	s, err := NewWithSessionDir("parent", testConfig(), home, sessionsRoot)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	runner := NewSubAgentRunner(s, 1)
	t.Cleanup(runner.SessionCancel)

	path, err := runner.subAgentDBPath("child")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(s.SessionDir(), "subagents", "child.db")
	if path != want {
		t.Fatalf("child DB path=%q want %q", path, want)
	}
	legacyRoot, _ := SessionsRoot()
	if strings.HasPrefix(path, legacyRoot+string(filepath.Separator)) {
		t.Fatalf("child DB leaked into root-session directory: %q", path)
	}
}

func TestDeriveSubAgentNameWhenModelOmitsName(t *testing.T) {
	if got := deriveSubAgentName(agent.CatResearcher, "Survey git state and identify logical commit groups"); got != "survey-git-state" {
		t.Fatalf("derived name = %q", got)
	}
	if got := deriveSubAgentName(agent.CatCrafter, "***"); got != "crafter-agent" {
		t.Fatalf("fallback name = %q", got)
	}
}
