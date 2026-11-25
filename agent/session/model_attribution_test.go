package session

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"crude/agent/provider"
	"crude/agent/sessiondb"
	"crude/internal/providersim"
	"crude/types/conversation"
	"crude/types/setting"
)

func TestModelSwitchAppliesToNextRequestWithoutRelabelingActiveTurn(t *testing.T) {
	requestedModels := make(chan string, 2)
	firstGate, secondGate := providersim.NewGate(), providersim.NewGate()
	server := providersim.New(t)
	server.Respond(func(request providersim.Request) providersim.Turn {
		requestedModels <- request.Model
		gate := firstGate
		if request.Index > 1 {
			gate = secondGate
		}
		return providersim.Text("reply from " + request.Model).WithoutUsage().WithGate(gate)
	})

	cfg := modelAttributionConfig(server.BaseURL(providersim.Chat), "model-a")
	home := t.TempDir()
	sessionDir := filepath.Join(t.TempDir(), "sessions")
	sess, err := NewWithSessionDir("model-attribution", cfg, home, sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	sender := newMockEventSender()
	orchestrator, err := sess.NewOrchestrator(sender)
	if err != nil {
		sess.Close()
		t.Fatal(err)
	}

	if !orchestrator.SubmitInput(provider.PlainInput("first")) {
		t.Fatal("first turn was not admitted")
	}
	if got := waitRequestedModel(t, requestedModels); got != "model-a" {
		t.Fatalf("first request model = %q, want model-a", got)
	}

	settings := sess.SettingsSnapshot()
	settings.Model = "model-b"
	settings.ProviderID = "openrouter"
	settings.SessionOverrides = map[string]setting.Category{
		CategoryDefault: {Provider: "openrouter", Model: "model-b", APIStandard: "openai"},
	}
	if err := sess.HandleSettingsSave(settings); err != nil {
		t.Fatalf("switch model while first request active: %v", err)
	}
	firstGate.Release()
	firstIdentity := waitTurnDone(t, sender, orchestrator.ID)
	if firstIdentity.ProviderID != "openrouter" || firstIdentity.ModelID != "model-a" || firstIdentity.Generation != 1 {
		t.Fatalf("first turn identity = %#v", firstIdentity)
	}
	waitAssistantCount(t, sess, 1)

	if !orchestrator.SubmitInput(provider.PlainInput("second")) {
		t.Fatal("second turn was not admitted")
	}
	if got := waitRequestedModel(t, requestedModels); got != "model-b" {
		t.Fatalf("second request model = %q, want model-b", got)
	}
	secondGate.Release()
	secondIdentity := waitTurnDone(t, sender, orchestrator.ID)
	if secondIdentity.ProviderID != "openrouter" || secondIdentity.ModelID != "model-b" || secondIdentity.Generation != 2 {
		t.Fatalf("second turn identity = %#v", secondIdentity)
	}
	waitAssistantCount(t, sess, 2)
	assertPersistedAssistantModels(t, sess.MessagesWithMeta(), []string{"model-a", "model-b"})
	dbPath := filepath.Join(sessionDir, sess.SessionID()+".db")
	sess.Close()

	resumed, err := ResumeSessionWithDir(dbPath, cfg, home, sessionDir)
	if err != nil {
		t.Fatalf("resume attributed session: %v", err)
	}
	defer resumed.Close()
	assertPersistedAssistantModels(t, resumed.MessagesWithMeta(), []string{"model-a", "model-b"})
}

func modelAttributionConfig(baseURL, model string) *Config {
	off := false
	return &Config{

		Accounts: map[string]provider.Account{"account": {ID: "account", ProviderID: "openrouter", APIKey: "test-key",
			Connection: provider.Template{
				Name: "OpenRouter", ProviderURL: baseURL, APIStandard: "openai", AuthType: provider.AuthProtocol,
			},
		}},
		Agents: AgentsConfig{
			Enabled: false, ActiveProfile: "default",
			Profiles: map[string]ProfileConfig{
				"default": {
					MultiAgent: &off,
					Categories: map[string]CategoryConfig{
						CategoryDefault: {AccountID: "account", Model: model},
					},
				},
			},
		},
	}
}

func waitRequestedModel(t *testing.T, models <-chan string) string {
	t.Helper()
	select {
	case model := <-models:
		return model
	case <-time.After(5 * time.Second):
		t.Fatal("provider request did not arrive")
		return ""
	}
}

func waitTurnDone(t *testing.T, sender *mockEventSender, agentID string, timeout ...time.Duration) conversation.TurnIdentity {
	t.Helper()
	budget := 5 * time.Second
	if len(timeout) > 0 {
		budget = timeout[0]
	}
	deadline := time.After(budget)
	var identity conversation.TurnIdentity
	var lastRecovery any
	for {
		select {
		case event := <-sender.ch:
			if event.agentID != agentID {
				continue
			}
			if event.typ == "turn_started" {
				identity, _ = event.data.(conversation.TurnIdentity)
			}
			if event.typ == "recovery" {
				lastRecovery = event.data
			}
			if event.typ == "done" {
				return identity
			}
		case <-deadline:
			t.Fatalf("turn did not complete; last recovery: %+v", lastRecovery)
			return conversation.TurnIdentity{}
		}
	}
}

func waitAssistantCount(t *testing.T, sess *Session, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		count := 0
		for _, message := range sess.MessagesWithMeta() {
			for _, snapshot := range message.Meta.Snapshots {
				if snapshot.Role == "assistant" {
					count++
				}
			}
		}
		if count >= want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("assistant count did not reach %d", want)
}

func assertPersistedAssistantModels(t *testing.T, messages []sessiondb.MessageWithMeta, want []string) {
	t.Helper()
	var got []string
	for _, message := range messages {
		for _, snapshot := range message.Meta.Snapshots {
			if snapshot.Role != "assistant" {
				continue
			}
			if snapshot.ProviderID != "openrouter" || snapshot.TurnGeneration == 0 {
				t.Fatalf("assistant attribution is incomplete: %#v", snapshot)
			}
			got = append(got, snapshot.ModelID)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("persisted assistant models = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("persisted assistant models = %v, want %v", got, want)
		}
	}
}

// Save through the session Settings owner while the first request is still in
// flight. Inspect the next real wire request, not only the saved form.
func TestRunningTurnReadsSessionCompactionWithoutRewritingPrefix(t *testing.T) {
	type payload struct {
		Messages []json.RawMessage `json:"messages"`
		Tools    json.RawMessage   `json:"tools"`
	}
	requests := make(chan providersim.Request, 4)
	gate := providersim.NewGate()
	server := providersim.New(t)
	server.Respond(func(request providersim.Request) providersim.Turn {
		requests <- request
		if request.Index == 1 {
			return providersim.CallTools(providersim.ToolCall{ID: "plan-1", Name: "get_plan", Arguments: "{}"}).WithoutUsage().WithGate(gate)
		}
		return providersim.Text("done").WithoutUsage()
	})
	sess, err := NewWithSessionDir("live-percent", modelAttributionConfig(server.BaseURL(providersim.Chat), "live-percent-model"), t.TempDir(), filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	sender := newMockEventSender()
	o, err := sess.NewOrchestrator(sender)
	if err != nil {
		t.Fatal(err)
	}
	if !o.SubmitInput(provider.PlainInput("Read the plan")) {
		t.Fatal("input not admitted")
	}
	next := func() payload {
		select {
		case request := <-requests:
			// The prefix comparison below checks exact wire fields, not equivalent text.
			var p payload
			_ = json.Unmarshal(request.Raw, &p)
			return p
		case <-time.After(8 * time.Second):
			t.Fatal("no provider request")
			return payload{}
		}
	}
	first := next()
	settings := sess.SettingsSnapshot()
	if settings.SessionOverrides == nil {
		settings.SessionOverrides = map[string]setting.Category{}
	}
	c := settings.SessionOverrides[CategoryDefault]
	v := 90
	c.Inherit = InheritWorkspace
	c.ContextCompactionPercent = &v
	settings.SessionOverrides[CategoryDefault] = c
	if err := sess.HandleSettingsSave(settings); err != nil {
		t.Fatal(err)
	}
	gate.Release()
	second := next()
	waitTurnDone(t, sender, o.ID)
	if len(second.Messages) <= len(first.Messages) || !reflect.DeepEqual(first.Messages, second.Messages[:len(first.Messages)]) || !bytes.Equal(first.Tools, second.Tools) {
		t.Fatal("passive Settings save changed the provider prefix")
	}
	data, err := os.ReadFile(filepath.Join(sess.logger.SessionDir(), "session.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte("live_settings_changed step=1 compaction_percent=90")) {
		t.Fatal("the running turn did not apply the 90% choice on its next step")
	}
}
