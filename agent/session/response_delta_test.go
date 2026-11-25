package session

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"crude/agent/provider"
	"crude/agent/sessiondb"
	"crude/internal/providersim"
	"crude/types/setting"
)

func TestResponsesDeltaSessionToolsTurnsAndPassiveResume(t *testing.T) {
	root, home, sessions := t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package actual_project\n"), 0600); err != nil {
		t.Fatal(err)
	}
	const model = "gpt-delta-e2e"
	provider.SetModelContext(model, 1_000_000)
	server := providersim.New(t)
	server.Respond(func(r providersim.Request) providersim.Turn {
		var turn providersim.Turn
		if r.Index == 1 {
			turn = simTool("read-1", "read_file", `{"path":"a.go"}`)
		} else {
			turn = providersim.Text(fmt.Sprintf("done-%d", r.Index))
		}
		return turn.WithUsage(providersim.Usage{Input: 100_000, Output: 10, Cached: 90_000})
	})
	modelRequests := func() []providersim.Request {
		var requests []providersim.Request
		for _, request := range server.Requests() {
			if request.Method == http.MethodPost {
				requests = append(requests, request)
			}
		}
		return requests
	}

	cfg := modelAttributionConfig(server.BaseURL(providersim.Responses), model)
	pc := cfg.Accounts["account"].Connection
	pc.APIStandard = "openai-responses"
	a := cfg.Accounts["account"]
	a.Connection = pc
	cfg.Accounts["account"] = a
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
	// Race/checkptr-instrumented modernc checkpoints of this 750 KB prefix
	// exceed the shared small-fixture wait; this is a protocol correctness test.
	const turnTimeout = 20 * time.Second
	if !orch.SubmitInput(provider.PlainInput("Inspect a.go. Retain this background:\n" + strings.Repeat("retained project context\n", 30_000))) {
		t.Fatal("input rejected")
	}
	waitTurnDone(t, sender, orch.ID, turnTimeout)
	waitAssistantCount(t, s, 1)
	if len(modelRequests()) != 2 {
		t.Fatalf("tool loop requests=%d", len(modelRequests()))
	}
	s.msgsMu.Lock()
	active := append([]provider.ChatMessage(nil), s.activeContext...)
	s.msgsMu.Unlock()
	requests := modelRequests()
	first := simDecodeRequest[struct{ Input []json.RawMessage }](t, requests[0])
	initialSystem := simDecodeRequest[struct {
		Role    string
		Content []struct{ Text string }
	}](t, providersim.Request{Raw: first.Input[0]})
	system := provider.NewTextMessage(initialSystem.Role, initialSystem.Content[0].Text)
	if requests[0].PreviousResponseID != "" {
		t.Fatal("first request did not seed complete context")
	}
	second := simDecodeRequest[struct {
		Input []struct {
			Type   string
			CallID string `json:"call_id"`
			Output string
		}
	}](t, requests[1])
	if len(second.Input) != 1 || requests[1].PreviousResponseID != simStoredResponseID(requests[0]) ||
		second.Input[0].Type != "function_call_output" || second.Input[0].CallID != "read-1" ||
		!strings.Contains(second.Input[0].Output, "actual_project") {
		t.Fatalf("missing exact real file-tool result: %+v", second.Input)
	}
	verify := func(active []provider.ChatMessage, last providersim.Request) {
		t.Helper()
		body, err := (&provider.OpenAIResponsesStandard{}).EncodeRequest(&provider.ChatRequest{Model: model, Messages: append([]provider.ChatMessage{system}, active...)})
		if err != nil {
			t.Fatal(err)
		}
		full := simDecodeRequest[struct{ Input []json.RawMessage }](t, providersim.Request{Raw: body})
		var effective []json.RawMessage
		for _, request := range modelRequests() {
			stored := fetchSimStoredResponse(t, server, request)
			input := simDecodeRequest[struct{ Input []json.RawMessage }](t, request)
			if stored.Previous != request.PreviousResponseID {
				t.Fatal("stored response did not retain its previous-response link")
			}
			if stored.Previous == "" {
				effective = nil
			}
			effective = append(effective, input.Input...)
			effective = append(effective, stored.Output...)
			if request.Index == last.Index {
				break
			}
		}
		expected, _ := json.Marshal(effective)
		actual, _ := json.Marshal(full.Input)
		var x, y any
		if json.Unmarshal(expected, &x) != nil || json.Unmarshal(actual, &y) != nil || !reflect.DeepEqual(x, y) {
			t.Fatal("delta-resolved server context differs from complete authoritative local context")
		}
	}
	verify(active, requests[1])
	state, found, err := sessiondb.LoadContextState(s.db, s.sessionID, s.sessionID)
	if err != nil || !found || state.Messages[len(state.Messages)-1].ResponseReference == nil {
		t.Fatalf("continuation was not durably persisted: found=%v err=%v", found, err)
	}
	beforeReopen := len(server.Requests())
	dbPath := s.dbPath
	s.Close()
	resumed, err := ResumeSessionWithDir(dbPath, cfg, home, sessions)
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()
	resumed.SetWorkDir(root)
	if len(server.Requests()) != beforeReopen {
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
	waitTurnDone(t, sender, orch.ID, turnTimeout)
	waitAssistantCount(t, resumed, 2)
	resumed.msgsMu.Lock()
	restored = append([]provider.ChatMessage(nil), resumed.activeContext...)
	resumed.msgsMu.Unlock()
	requests = modelRequests()
	if len(requests) != 3 || requests[2].PreviousResponseID != simStoredResponseID(requests[1]) ||
		len(simDecodeRequest[struct{ Input []json.RawMessage }](t, requests[2]).Input) > 3 {
		t.Fatal("resumed turn uploaded old history rather than the retained delta")
	}
	verify(restored, requests[2])
	const otherModel = "gpt-other-delta-e2e"
	provider.SetModelContext(otherModel, 1_000_000)
	settings := resumed.SettingsSnapshot()
	settings.Model = otherModel
	settings.ProviderID = "openrouter"
	settings.SessionOverrides = map[string]setting.Category{CategoryDefault: {Provider: "openrouter", Model: otherModel, APIStandard: "openai-responses"}}
	if err := resumed.HandleSettingsSave(settings); err != nil {
		t.Fatal(err)
	}
	for count := 3; count <= 4; count++ {
		if !orch.SubmitInput(provider.PlainInput("Continue on the selected model.")) {
			t.Fatal("model-switched input rejected")
		}
		waitTurnDone(t, sender, orch.ID, turnTimeout)
		waitAssistantCount(t, resumed, count)
	}
	requests = modelRequests()
	if len(requests) != 5 {
		t.Fatalf("want five requests, got %d", len(requests))
	}
	sizes := make([]int, 5)
	for i, request := range requests {
		sizes[i] = len(request.Raw)
	}
	if requests[3].PreviousResponseID != "" || sizes[3] < sizes[0]/2 {
		t.Fatal("model change incorrectly reused another model's response")
	}
	if requests[4].PreviousResponseID != simStoredResponseID(requests[3]) ||
		len(simDecodeRequest[struct{ Input []json.RawMessage }](t, requests[4]).Input) > 3 {
		t.Fatal("new model did not establish its own delta chain")
	}
	if sizes[1]*4 >= sizes[0] || sizes[2]*4 >= sizes[0] || sizes[4]*4 >= sizes[3] {
		t.Fatalf("insufficient actual upload reduction: %v", sizes)
	}
	t.Logf("HTTP payload bytes: initial=%d tool delta=%d resumed-turn delta=%d", sizes[0], sizes[1], sizes[2])
}

// The simulator journals the request index used in its deterministic response ID.
func simStoredResponseID(r providersim.Request) string { return fmt.Sprintf("resp_sim_%d", r.Index) }

type simStoredResponse struct {
	Previous string `json:"previous_response_id"`
	Output   []json.RawMessage
}

func fetchSimStoredResponse(t *testing.T, server *providersim.Server, r providersim.Request) simStoredResponse {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet,
		server.BaseURL(providersim.Responses)+"/responses/"+simStoredResponseID(r), nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var stored simStoredResponse
	if response.StatusCode != http.StatusOK {
		t.Fatalf("fetch stored response %s: %s", simStoredResponseID(r), response.Status)
	}
	if err := json.NewDecoder(response.Body).Decode(&stored); err != nil {
		t.Fatal(err)
	}
	return stored
}
