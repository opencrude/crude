package session

import (
	"testing"

	"crude/provider"
)

type mockEventSender struct {
	ch chan agentEvent
}

type agentEvent struct {
	typ     string
	data    any
	agentID string
}

func newMockEventSender() *mockEventSender {
	return &mockEventSender{ch: make(chan agentEvent, 100)}
}

func (m *mockEventSender) SendAgentEvent(typ string, data any) {
	m.ch <- agentEvent{typ: typ, data: data}
}

func (m *mockEventSender) SendAgentEventWithID(typ string, data any, agentID string) {
	m.ch <- agentEvent{typ: typ, data: data, agentID: agentID}
}

func testConfig() *Config {
	return &Config{
		ActiveProvider: "openrouter",
		Providers: map[string]ProviderConfig{
			"openrouter": {
				APIKey:      "test-key",
				ProviderURL: "https://openrouter.ai/api/v1",
			},
		},
		Agents: DefaultAgentsConfig(),
	}
}

func TestNewSessionManager(t *testing.T) {
	sm := NewSessionManager()
	if sm == nil {
		t.Fatal("NewSessionManager returned nil")
	}
	if sm.Count() != 0 {
		t.Fatalf("expected 0 sessions, got %d", sm.Count())
	}
}

func TestNewSession(t *testing.T) {
	sm := NewSessionManager()
	cfg := testConfig()

	info, err := sm.NewSession(cfg, t.TempDir())
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}

	if info.ID == "" {
		t.Fatal("expected non-empty session ID")
	}
	if info.Session == nil {
		t.Fatal("expected non-nil session")
	}
	if info.Orchestrator != nil {
		t.Fatal("expected nil orchestrator on new session")
	}
	if sm.Count() != 1 {
		t.Fatalf("expected 1 session, got %d", sm.Count())
	}
}

func TestStartOrchestrator(t *testing.T) {
	sm := NewSessionManager()
	cfg := testConfig()

	info, err := sm.NewSession(cfg, t.TempDir())
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}

	sender := newMockEventSender()
	orch, err := sm.StartOrchestrator(info.ID, sender)
	if err != nil {
		t.Fatalf("StartOrchestrator failed: %v", err)
	}

	if orch == nil {
		t.Fatal("expected non-nil orchestrator")
	}
	if orch.ID == "" {
		t.Fatal("expected non-empty orchestrator ID")
	}
	if info.Orchestrator == nil {
		t.Fatal("expected info.Orchestrator to be set")
	}
	if info.Orchestrator.ID != orch.ID {
		t.Fatalf("expected orchid %q, got %q", orch.ID, info.Orchestrator.ID)
	}
	if orch.InputCh == nil {
		t.Fatal("expected non-nil InputCh")
	}
	if orch.BlockResolveCh == nil {
		t.Fatal("expected non-nil BlockResolveCh")
	}
}

func TestStartOrchestratorNonexistent(t *testing.T) {
	sm := NewSessionManager()
	sender := newMockEventSender()

	_, err := sm.StartOrchestrator("nonexistent", sender)
	if err == nil {
		t.Fatal("expected error for nonexistent session")
	}
}

func TestSessionIDIncrement(t *testing.T) {
	sm := NewSessionManager()
	cfg := testConfig()
	home := t.TempDir()

	info1, _ := sm.NewSession(cfg, home)
	info2, _ := sm.NewSession(cfg, home)

	if info1.ID == info2.ID {
		t.Fatalf("expected different session IDs, got both %q", info1.ID)
	}
}

func TestNewSessionIDFormat(t *testing.T) {
	sm := NewSessionManager()
	cfg := testConfig()

	info, err := sm.NewSession(cfg, t.TempDir())
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}

	if len(info.ID) < 5 || info.ID[:4] != "ses_" {
		t.Fatalf("expected session ID to start with 'ses_', got %q", info.ID)
	}
}

func TestGetSession(t *testing.T) {
	sm := NewSessionManager()
	cfg := testConfig()

	created, _ := sm.NewSession(cfg, t.TempDir())

	got, ok := sm.Get(created.ID)
	if !ok {
		t.Fatal("expected to find session by ID")
	}
	if got.ID != created.ID {
		t.Fatalf("expected ID %q, got %q", created.ID, got.ID)
	}

	_, ok = sm.Get("nonexistent")
	if ok {
		t.Fatal("expected false for nonexistent session")
	}
}

func TestRemoveSession(t *testing.T) {
	sm := NewSessionManager()
	cfg := testConfig()

	info, _ := sm.NewSession(cfg, t.TempDir())
	id := info.ID

	sm.Remove(id)

	if sm.Count() != 0 {
		t.Fatalf("expected 0 sessions after remove, got %d", sm.Count())
	}

	_, ok := sm.Get(id)
	if ok {
		t.Fatal("expected session to be removed")
	}
}

func TestRemoveNonexistent(t *testing.T) {
	sm := NewSessionManager()
	sm.Remove("nonexistent")
	if sm.Count() != 0 {
		t.Fatalf("expected 0 sessions, got %d", sm.Count())
	}
}

func TestFirstSession(t *testing.T) {
	sm := NewSessionManager()

	info := sm.First()
	if info != nil {
		t.Fatal("expected nil for empty manager")
	}

	cfg := testConfig()
	created, _ := sm.NewSession(cfg, t.TempDir())
	first := sm.First()
	if first == nil {
		t.Fatal("expected non-nil First()")
	}
	if first.ID != created.ID {
		t.Fatalf("expected ID %q, got %q", created.ID, first.ID)
	}
}

func TestCloseAll(t *testing.T) {
	sm := NewSessionManager()
	cfg := testConfig()
	home := t.TempDir()

	sm.NewSession(cfg, home)
	sm.NewSession(cfg, home)
	sm.NewSession(cfg, home)

	if sm.Count() != 3 {
		t.Fatalf("expected 3 sessions, got %d", sm.Count())
	}

	sm.CloseAll()

	if sm.Count() != 0 {
		t.Fatalf("expected 0 sessions after CloseAll, got %d", sm.Count())
	}
}

func TestCount(t *testing.T) {
	sm := NewSessionManager()
	cfg := testConfig()
	home := t.TempDir()

	if sm.Count() != 0 {
		t.Fatalf("expected 0, got %d", sm.Count())
	}

	sm.NewSession(cfg, home)
	if sm.Count() != 1 {
		t.Fatalf("expected 1, got %d", sm.Count())
	}

	sm.NewSession(cfg, home)
	if sm.Count() != 2 {
		t.Fatalf("expected 2, got %d", sm.Count())
	}

	sm.Remove("nonexistent")
	if sm.Count() != 2 {
		t.Fatalf("expected 2 after remove nonexistent, got %d", sm.Count())
	}
}

func TestOrchestratorChannels(t *testing.T) {
	sm := NewSessionManager()
	cfg := testConfig()
	home := t.TempDir()

	info, _ := sm.NewSession(cfg, home)
	sender := newMockEventSender()
	orch, _ := sm.StartOrchestrator(info.ID, sender)

	// Verify orchestrator channels are functional
	inputCh := orch.InputCh
	btwCh := orch.BtwCh
	cancelCh := orch.CancelCh
	blockResolveCh := orch.BlockResolveCh

	if inputCh == nil || btwCh == nil || cancelCh == nil || blockResolveCh == nil {
		t.Fatal("orchestrator channels should not be nil")
	}

	// Send on orch's channel
	inputCh <- provider.UserInput{Text: "test"}
	btwCh <- provider.UserInput{Text: "btw"}

	// Don't read from cancelCh or blockResolveCh (they block)
	// Just verify they were created properly
}

// TestResumedSessionOrchestrators verifies that a resumed session's
// orchestrators map is initialized (not nil), so NewOrchestrator doesn't panic.
func TestResumedSessionOrchestrators(t *testing.T) {
	sm := NewSessionManager()
	cfg := testConfig()
	home := t.TempDir()

	info, err := sm.NewSession(cfg, home)
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	sess := info.Session

	// Manually clear the orchestrators map to simulate a resume path
	// that forgets to initialize it (as was the case in the bug).
	sess.orchestrators = nil

	sender := newMockEventSender()
	orch, err := sess.NewOrchestrator(sender)
	if err != nil {
		t.Fatalf("NewOrchestrator failed: %v", err)
	}
	if orch == nil {
		t.Fatal("expected non-nil orchestrator")
	}
	if sess.orchestrators == nil {
		t.Fatal("NewOrchestrator should initialize orchestrators map if nil")
	}
	if _, ok := sess.orchestrators[orch.ID]; !ok {
		t.Fatal("orchestrator should be stored in map")
	}
}

func TestCloseOrchestratorSignalsStopWithoutClosingInputChannels(t *testing.T) {
	sess := &Session{
		orchestrators:  make(map[string]*Orchestrator),
		runtimeProfile: NewRuntimeProfile(testConfig()),
	}
	orch := NewOrchestrator("orch", sess)
	sess.orchestrators[orch.ID] = orch
	sess.runtimeProfile.GetOrCreateOverride(orch.ID)

	sess.CloseOrchestrator(orch.ID)
	sess.CloseOrchestrator(orch.ID)

	select {
	case <-orch.stopCh:
	default:
		t.Fatal("CloseOrchestrator() did not close the private stop channel")
	}

	// These channels are owned by senders outside session. They must remain
	// open so an already-scheduled Bubble Tea command cannot panic on send.
	orch.InputCh <- provider.PlainInput("late input")
	orch.BtwCh <- provider.PlainInput("late btw")
}
