package session

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"crude/provider"
	"crude/sessiondb"
	ttypes "crude/types"
)

func TestModelSwitchAppliesToNextRequestWithoutRelabelingActiveTurn(t *testing.T) {
	requestedModels := make(chan string, 2)
	releaseRequest := make(chan struct{}, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		requestedModels <- body.Model
		<-releaseRequest
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w,
			"data: {\"choices\":[{\"delta\":{\"content\":%q},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n",
			"reply from "+body.Model,
		)
	}))
	defer server.Close()
	defer close(releaseRequest)

	cfg := modelAttributionConfig(server.URL, "model-a")
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
	settings.SessionOverrides = map[string]ttypes.CategorySettings{
		CategoryDefault: {Provider: "openrouter", Model: "model-b", APIStandard: "openai"},
	}
	if err := sess.HandleSettingsSave(settings); err != nil {
		t.Fatalf("switch model while first request active: %v", err)
	}
	releaseRequest <- struct{}{}
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
	releaseRequest <- struct{}{}
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
		ActiveProvider: "openrouter",
		Accounts:       map[string]provider.Account{"account": {ID: "account", ProviderID: "openrouter", APIKey: "test-key"}},
		Providers: map[string]ProviderConfig{
			"openrouter": {
				Name: "OpenRouter", ProviderURL: baseURL, APIStandard: "openai", AuthType: provider.AuthProtocol,
			},
		},
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

func waitTurnDone(t *testing.T, sender *mockEventSender, agentID string) ttypes.TurnIdentity {
	t.Helper()
	deadline := time.After(5 * time.Second)
	var identity ttypes.TurnIdentity
	for {
		select {
		case event := <-sender.ch:
			if event.agentID != agentID {
				continue
			}
			if event.typ == "turn_started" {
				identity, _ = event.data.(ttypes.TurnIdentity)
			}
			if event.typ == "done" {
				return identity
			}
		case <-deadline:
			t.Fatal("turn did not complete")
			return ttypes.TurnIdentity{}
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
