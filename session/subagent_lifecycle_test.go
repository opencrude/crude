package session

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"crude/agent"
	"crude/provider"
	"crude/sessiondb"
	"crude/tools"
)

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
	handle := &agent.SubAgentHandle{
		DB: db, SessionID: "child",
		Spec: agent.SubAgentSpec{Provider: "ollama-cloud", Model: "glm-5.3-flash:cloud"},
	}
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
		withMeta[2].Meta.Snapshots[0].ToolCommand != "go test ./..." ||
		withMeta[2].Meta.Snapshots[0].ProviderID != "ollama-cloud" ||
		withMeta[2].Meta.Snapshots[0].ModelID != "glm-5.3-flash:cloud" ||
		withMeta[3].Meta.Snapshots[0].ModelID != "glm-5.3-flash:cloud" {
		t.Fatalf("canonical child transcript metadata = %+v", withMeta)
	}
}

func TestEverySubAgentTerminalPathPublishesOneEventAndResult(t *testing.T) {
	s := &Session{}
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
	if _, ok := runner.Manager().GetActive(handle.Spec.ID); ok {
		t.Fatal("terminal child remained active")
	}
}

func TestSubAgentToolHasNoRuntimeDeadline(t *testing.T) {
	for _, tool := range tools.CollaborationTools {
		if tool.Function.Name == "agent_start" {
			if strings.Contains(string(tool.Function.Parameters), "timeout_seconds") {
				t.Fatal("sub-agent launch still exposes a runtime deadline")
			}
			return
		}
	}
	t.Fatal("agent_start missing")
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

func TestNamedSubAgentProfileCanDisableAutomaticCompaction(t *testing.T) {
	cfg := testConfig()
	base, disabled := 85, 0
	profile := cfg.Agents.Profiles[cfg.Agents.ActiveProfile]
	researcher := profile.Categories[CategoryResearcher]
	researcher.ContextCompactionPercent = &base
	profile.Categories[CategoryResearcher] = researcher
	cfg.Agents.Profiles[cfg.Agents.ActiveProfile] = profile
	if cfg.Agents.AgentProfiles == nil {
		cfg.Agents.AgentProfiles = make(map[string]AgentProfileConfig)
	}
	cfg.Agents.AgentProfiles["no-compact"] = AgentProfileConfig{ContextCompactionPercent: &disabled}
	runner := NewSubAgentRunner(&Session{cfg: cfg}, 1)
	t.Cleanup(runner.SessionCancel)
	spec := agent.SubAgentSpec{Name: "no-compact", Category: agent.CatResearcher}
	runner.resolveProfile(spec.Name, spec.Category, &spec)
	runner.applyCategoryDefaults(&spec)
	if spec.ContextCompactionPercent == nil || *spec.ContextCompactionPercent != 0 {
		t.Fatalf("named profile disable was overwritten: %#v", spec.ContextCompactionPercent)
	}
}
