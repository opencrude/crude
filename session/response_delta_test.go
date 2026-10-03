package session

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"crude/provider"
	"crude/sessiondb"
	ttypes "crude/types"
)

func TestResponsesDeltaSessionToolsTurnsAndPassiveResume(t *testing.T) {
	root, home, sessions := t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package actual_project\n"), 0600); err != nil {
		t.Fatal(err)
	}
	const model = "gpt-delta-e2e"
	provider.SetModelContext(model, 1_000_000)
	var requests atomic.Int32
	var mu sync.Mutex
	stored := make(map[string][]json.RawMessage)
	var sizes []int
	var initialSystem provider.ChatMessage
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		var wire struct {
			Previous string            `json:"previous_response_id"`
			Input    []json.RawMessage `json:"input"`
		}
		if err := json.Unmarshal(body, &wire); err != nil {
			t.Error(err)
			return
		}
		index := int(requests.Add(1))
		mu.Lock()
		defer mu.Unlock()
		sizes = append(sizes, len(body))
		effective := append([]json.RawMessage(nil), wire.Input...)
		if wire.Previous != "" {
			prefix, ok := stored[wire.Previous]
			if !ok {
				t.Error("unknown previous response")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			effective = append(append([]json.RawMessage(nil), prefix...), wire.Input...)
		}
		switch index {
		case 1:
			if wire.Previous != "" {
				t.Error("first request did not seed complete context")
			}
			var system struct {
				Role    string
				Content []struct{ Text string }
			}
			if err := json.Unmarshal(wire.Input[0], &system); err != nil {
				t.Error(err)
				return
			}
			initialSystem = provider.NewTextMessage(system.Role, system.Content[0].Text)
		case 2:
			var result struct {
				Type   string
				CallID string `json:"call_id"`
				Output string
			}
			if len(wire.Input) != 1 {
				t.Errorf("tool round re-uploaded %d input items", len(wire.Input))
			} else if err := json.Unmarshal(wire.Input[0], &result); err != nil {
				t.Error(err)
			}
			if wire.Previous != "response-1" || result.CallID != "read-1" || !strings.Contains(result.Output, "actual_project") {
				t.Errorf("missing exact real file-tool result: %+v", result)
			}
		case 3:
			if wire.Previous != "response-2" || len(wire.Input) > 3 {
				t.Errorf("resumed turn uploaded old history: previous=%q items=%d", wire.Previous, len(wire.Input))
			}
		case 4:
			if wire.Previous != "" || len(body) < sizes[0]/2 {
				t.Error("model change incorrectly reused another model's response")
			}
		case 5:
			if wire.Previous != "response-4" || len(wire.Input) > 3 {
				t.Error("new model did not establish its own delta chain")
			}
		}
		var output json.RawMessage
		if index == 1 {
			output = json.RawMessage(`{"type":"function_call","id":"fc-1","name":"read_file","call_id":"read-1","arguments":"{\"path\":\"a.go\"}","status":"completed"}`)
		} else {
			output = json.RawMessage(fmt.Sprintf(`{"type":"message","id":"m-%d","role":"assistant","status":"completed","content":[{"type":"output_text","text":"done-%d"}]}`, index, index))
		}
		stored[fmt.Sprintf("response-%d", index)] = append(effective, output)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "data: {\"type\":\"response.created\",\"response\":{\"id\":\"response-%d\",\"status\":\"in_progress\",\"store\":true,\"output\":[]}}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"store\":true,\"output\":[%s],\"usage\":{\"input_tokens\":100000,\"output_tokens\":10,\"input_tokens_details\":{\"cached_tokens\":90000}}}}\n\n", index, output)
	}))
	defer server.Close()
	cfg := modelAttributionConfig(server.URL, model)
	pc := cfg.Providers["openrouter"]
	pc.APIStandard = "openai-responses"
	cfg.Providers["openrouter"] = pc
	s, err := NewWithSessionDir("delta-session", cfg, home, sessions)
	if err != nil {
		t.Fatal(err)
	}
	s.SetWorkDir(root)
	t.Cleanup(s.Close)
	sender := newMockEventSender()
	orch, err := s.NewOrchestrator(sender)
	if err != nil {
		t.Fatal(err)
	}
	if !orch.SubmitInput(provider.PlainInput("Inspect a.go. Retain this background:\n" + strings.Repeat("retained project context\n", 30_000))) {
		t.Fatal("input rejected")
	}
	waitTurnDone(t, sender, orch.ID)
	waitAssistantCount(t, s, 1)
	if requests.Load() != 2 {
		t.Fatalf("tool loop requests=%d", requests.Load())
	}
	s.msgsMu.Lock()
	active := append([]provider.ChatMessage(nil), s.activeContext...)
	s.msgsMu.Unlock()
	verify := func(active []provider.ChatMessage, id string) {
		t.Helper()
		body, err := (&provider.OpenAIResponsesStandard{}).EncodeRequest(&provider.ChatRequest{Model: model, Messages: append([]provider.ChatMessage{initialSystem}, active...)})
		if err != nil {
			t.Fatal(err)
		}
		var full struct {
			Input []json.RawMessage `json:"input"`
		}
		if err := json.Unmarshal(body, &full); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		expected, _ := json.Marshal(stored[id])
		mu.Unlock()
		actual, _ := json.Marshal(full.Input)
		var x, y any
		if json.Unmarshal(expected, &x) != nil || json.Unmarshal(actual, &y) != nil || !reflect.DeepEqual(x, y) {
			t.Fatal("delta-resolved server context differs from complete authoritative local context")
		}
	}
	verify(active, "response-2")
	state, found, err := sessiondb.LoadContextState(s.db, s.sessionID, s.sessionID)
	if err != nil || !found || state.Messages[len(state.Messages)-1].ResponseReference == nil {
		t.Fatalf("continuation was not durably persisted: found=%v err=%v", found, err)
	}
	dbPath := s.dbPath
	s.Close()
	resumed, err := ResumeSessionWithDir(dbPath, cfg, home, sessions)
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()
	resumed.SetWorkDir(root)
	if requests.Load() != 2 {
		t.Fatal("passive resume made a provider request")
	}
	resumed.msgsMu.Lock()
	restored := append([]provider.ChatMessage(nil), resumed.activeContext...)
	resumed.msgsMu.Unlock()
	oldBytes, _ := json.Marshal(active)
	newBytes, _ := json.Marshal(restored)
	if string(oldBytes) != string(newBytes) {
		t.Fatal("resume changed exact context or continuation identity")
	}
	sender = newMockEventSender()
	orch, err = resumed.NewOrchestrator(sender)
	if err != nil {
		t.Fatal(err)
	}
	if !orch.SubmitInput(provider.PlainInput("Continue with the next small request.")) {
		t.Fatal("resumed input rejected")
	}
	waitTurnDone(t, sender, orch.ID)
	waitAssistantCount(t, resumed, 2)
	resumed.msgsMu.Lock()
	restored = append([]provider.ChatMessage(nil), resumed.activeContext...)
	resumed.msgsMu.Unlock()
	verify(restored, "response-3")
	const otherModel = "gpt-other-delta-e2e"
	provider.SetModelContext(otherModel, 1_000_000)
	settings := resumed.SettingsSnapshot()
	settings.Model = otherModel
	settings.ProviderID = "openrouter"
	settings.SessionOverrides = map[string]ttypes.CategorySettings{CategoryDefault: {Provider: "openrouter", Model: otherModel, APIStandard: "openai-responses"}}
	if err := resumed.HandleSettingsSave(settings); err != nil {
		t.Fatal(err)
	}
	for count := 3; count <= 4; count++ {
		if !orch.SubmitInput(provider.PlainInput("Continue on the selected model.")) {
			t.Fatal("model-switched input rejected")
		}
		waitTurnDone(t, sender, orch.ID)
		waitAssistantCount(t, resumed, count)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(sizes) != 5 || sizes[1]*4 >= sizes[0] || sizes[2]*4 >= sizes[0] || sizes[4]*4 >= sizes[3] {
		t.Fatalf("insufficient actual upload reduction: %v", sizes)
	}
	t.Logf("HTTP payload bytes: initial=%d tool delta=%d resumed-turn delta=%d", sizes[0], sizes[1], sizes[2])
}
