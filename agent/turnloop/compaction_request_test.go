package turnloop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"crude/agent/provider"
)

// A human request compacts before the next model request even when
// automatic compaction is off and the context is far from any threshold.
func TestRunHonoursHumanCompactionRequestWithAutomaticCompactionOff(t *testing.T) {
	var requests atomic.Int32
	var bodies [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		bodies = append(bodies, body)
		w.Header().Set("Content-Type", "application/x-ndjson")
		if requests.Add(1) <= int32(len(compactionStagesForTest)) {
			encoded, _ := json.Marshal("- compact stage: preserved facts")
			_, _ = fmt.Fprintf(w, `{"model":"llama3","message":{"role":"assistant","content":%s},"done":true,"done_reason":"stop","prompt_eval_count":1000,"eval_count":10}`+"\n", encoded)
			return
		}
		_, _ = fmt.Fprint(w, `{"model":"llama3","message":{"role":"assistant","content":"done"},"done":true,"done_reason":"stop","prompt_eval_count":1000,"eval_count":1}`+"\n")
	}))
	defer server.Close()
	p := provider.FromPreset("ollama-cloud", "ollama-key", server.URL)
	p.Model = "compaction-request-model"
	provider.SetModelContext(p.Model, 1_000_000)
	a := newTestAgent(t, p, p.Model)
	a.ContextCompactionPercent = CompactionOff
	taken := 0
	a.CompactionRequest = func() string {
		taken++
		if taken == 1 {
			return "human-request"
		}
		return ""
	}
	var history []provider.ChatMessage
	for i := range 6 {
		history = append(history,
			provider.NewTextMessage("user", fmt.Sprintf("request %d %s", i, strings.Repeat("context ", 400))),
			provider.NewTextMessage("assistant", fmt.Sprintf("answer %d %s", i, strings.Repeat("analysis ", 400))))
	}
	history = append(history, provider.NewTextMessage("user", "current instruction must survive"))
	var compacted map[string]any
	a.Run(context.Background(), history, "system", func(event AgentEvent) {
		if event.Type == "context_compacted" {
			compacted, _ = event.Data.(map[string]any)
		}
	}, nil, "", nil)
	if compacted == nil || compacted["trigger"] != "requested" || compacted["request_id"] != "human-request" {
		t.Fatalf("requested compaction did not run: %+v (requests=%d)", compacted, requests.Load())
	}
	final := string(bodies[len(bodies)-1])
	if strings.Contains(final, "answer 5 ") || !strings.Contains(final, "context_checkpoint") || strings.Contains(final, "request 0 ") || !strings.Contains(final, "current instruction must survive") {
		t.Fatal("the next model request did not use the compacted context")
	}
}

// A compaction choice the user makes while a run works applies at the next
// step. The run starts with automatic compaction off; the owner's current
// settings ask for 5%, so the step compacts and adopts the other passive
// settings without waiting for another run.
func TestRunAppliesLiveSettingsBeforeEachStep(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/x-ndjson")
		if requests.Add(1) <= int32(len(compactionStagesForTest)) {
			encoded, _ := json.Marshal("- compact stage: preserved facts")
			_, _ = fmt.Fprintf(w, `{"model":"llama3","message":{"role":"assistant","content":%s},"done":true,"done_reason":"stop","prompt_eval_count":1000,"eval_count":10}`+"\n", encoded)
			return
		}
		_, _ = fmt.Fprint(w, `{"model":"llama3","message":{"role":"assistant","content":"done"},"done":true,"done_reason":"stop","prompt_eval_count":1000,"eval_count":1}`+"\n")
	}))
	defer server.Close()
	p := provider.FromPreset("ollama-cloud", "ollama-key", server.URL)
	p.Model = "live-settings-model"
	provider.SetModelContext(p.Model, 100_000)
	a := newTestAgent(t, p, p.Model)
	a.ContextCompactionPercent = CompactionOff
	reads := 0
	a.LiveSettings = func() LiveSettings {
		reads++
		return LiveSettings{CompactionPercent: 5, RetentionTokens: a.CompactionRetentionTokens, MaxOutputTokens: 4096,
			CompactionProvider: a.CompactionProvider, CompactionMaxOutput: a.CompactionMaxOutput}
	}
	var history []provider.ChatMessage
	for i := range 6 {
		history = append(history,
			provider.NewTextMessage("user", fmt.Sprintf("request %d %s", i, strings.Repeat("context ", 1200))),
			provider.NewTextMessage("assistant", fmt.Sprintf("answer %d %s", i, strings.Repeat("analysis ", 1200))))
	}
	history = append(history, provider.NewTextMessage("user", "current instruction must survive"))
	var compacted map[string]any
	a.Run(context.Background(), history, "system", func(event AgentEvent) {
		if event.Type == "context_compacted" {
			compacted, _ = event.Data.(map[string]any)
		}
	}, nil, "", nil)
	if reads == 0 || a.ContextCompactionPercent != 5 || a.MaxOutputTokens != 4096 {
		t.Fatalf("live settings not adopted: reads=%d percent=%d max_output=%d", reads, a.ContextCompactionPercent, a.MaxOutputTokens)
	}
	if compacted == nil || compacted["trigger"] != "automatic" {
		t.Fatalf("the live 5%% choice did not compact: %+v (requests=%d)", compacted, requests.Load())
	}
}

// The wire is Responses, the main model is Claude, and the summarizer uses a
// separate Chat connection. Only synthetic input may reach discovery.
func TestCustomResponsesColdCheckpoint(t *testing.T) {
	var mainCalls atomic.Int32
	const old = "long historical assistant output: "
	const instruction = "Continue checking the unresolved build error."
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/responses/compact" {
			b, _ := io.ReadAll(r.Body)
			if strings.Contains(string(b), old) || strings.Contains(string(b), instruction) {
				t.Error("probe received real history")
			}
			http.NotFound(w, r)
			return
		}
		var body struct {
			Model    string
			Input    json.RawMessage
			Previous string `json:"previous_response_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		if strings.Contains(string(body.Input), "compaction_trigger") {
			if strings.Contains(string(body.Input), old) || strings.Contains(string(body.Input), instruction) {
				t.Fatal("trigger probe exposed conversation")
			}
			http.Error(w, `{"error":{"message":"unsupported compaction trigger"}}`, http.StatusBadRequest)
			return
		}
		mainCalls.Add(1)
		if r.URL.Path != "/v1/responses" || body.Model != "claude-gateway-test" || body.Previous != "" || !strings.Contains(string(body.Input), "context_checkpoint") || !strings.Contains(string(body.Input), instruction) || strings.Contains(string(body.Input), old) {
			t.Errorf("unexpected cold request: path=%s model=%s bytes=%d anchor=%s", r.URL.Path, body.Model, len(body.Input), body.Previous)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}]}}`+"\n\n")
	}))
	defer gateway.Close()
	// A real tool-free extraction endpoint, not an injected compact outcome.
	recorder := &precompactionServer{}
	summary := httptest.NewServer(recorder.handler(t))
	defer summary.Close()
	p := &provider.Provider{ID: "custom", StandardName: "openai-responses", Model: "claude-gateway-test", BaseURL: gateway.URL + "/v1"}
	p.SetContextWindow(100_000)
	a := newTestAgent(t, p, p.Model)
	a.CompactionProvider = provider.FromPreset("openai-chat", "key", summary.URL)
	a.CompactionProvider.Model = "extractor-test"
	a.CompactionProvider.SetContextWindow(200_000)
	a.ContextCompactionPercent = 10
	var history []provider.ChatMessage
	for i := range 14 {
		history = append(history, provider.NewTextMessage("user", fmt.Sprintf("step %d", i)), provider.NewTextMessage("assistant", old+strings.Repeat("payload ", 1400)))
	}
	history = append(history, provider.NewTextMessage("user", instruction))
	var archived []provider.ChatMessage
	a.PrepareCompaction = func(source CompactionSource) error { archived = source.Messages; return nil }
	var events []AgentEvent
	a.Run(t.Context(), history, "system", func(e AgentEvent) { events = append(events, e) }, nil, "", nil)
	if mainCalls.Load() != 1 || recorder.compactions.Load() != 1 || !hasEvent(events, "context_compacted") || hasEvent(events, "error") || len(archived) < len(history) || !strings.Contains(archived[1].ContentString(), old) {
		t.Fatalf("main=%d extractions=%d archived=%d", mainCalls.Load(), recorder.compactions.Load(), len(archived))
	}
}

func TestAutomaticNativeCheckpointRecoversRejectedArtifactBeforeOutput(t *testing.T) {
	var paid, normal atomic.Int32
	var observedTicket string
	const handoff = "<<<intent>>>\nKeep the build passing\n<<<work>>>\nChecked inputs\n<<<state>>>\nTarget is blue\n<<<continuation>>>\nCheck the target"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		s := string(raw)
		var body struct {
			Instructions string
			Input        json.RawMessage
			Model        string
			Stream       bool
		}
		_ = json.Unmarshal(raw, &body)
		if body.Model != "fixture-native" {
			t.Error("provider-only switched models")
		}
		if strings.HasSuffix(r.URL.Path, "/compact") {
			token := "secret-real"
			if strings.Contains(s, "old assistant evidence") {
				paid.Add(1)
			} else {
				parts := strings.Fields(body.Instructions)
				if len(parts) < 5 {
					t.Error("missing probe code")
					return
				}
				observedTicket = parts[4]
				token = "secret-probe"
			}
			fmt.Fprintf(w, `{"object":"response.compaction","output":[{"type":"compaction","encrypted_content":%q}]}`, token)
			return
		}
		if strings.Contains(s, "old assistant evidence") {
			t.Error("old history replayed")
		}
		if !body.Stream {
			answer := handoff
			if strings.Contains(s, "secret-probe") {
				answer = observedTicket
			}
			fmt.Fprintf(w, `{"object":"response","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":%q}]}]}`, strings.TrimSuffix(answer, ","))
			return
		}
		if normal.Add(1) == 1 {
			if !strings.Contains(s, "secret-real") {
				t.Error("missing native window on main request")
			}
			http.Error(w, `{"error":{"message":"invalid compaction item"}}`, http.StatusBadRequest)
			return
		}
		if strings.Contains(s, "secret-real") || !strings.Contains(s, "context_checkpoint") || !strings.Contains(s, "Target is blue") {
			t.Error("main retry did not use portable state")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_done\",\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"done\"}]}]}}\n\n")
	}))
	defer server.Close()
	p := &provider.Provider{ID: "custom", AccountID: "native-e2e", StandardName: "openai-responses", Model: "fixture-native", BaseURL: server.URL}
	p.SetProtocolSettings(&provider.ProtocolSettings{Compaction: provider.CompactionProviderOnly})
	p.SetContextWindow(100000)
	a := newTestAgent(t, p, p.Model)
	a.ContextCompactionPercent = 20
	history := []provider.ChatMessage{provider.NewTextMessage("assistant", strings.Repeat("old assistant evidence ", 4500)), provider.NewTextMessage("user", "Keep the build passing")}
	var commits int
	var events []AgentEvent
	a.CommitCompaction = func(c CompactionCommit) error {
		commits++
		if len(c.Messages[0].ProviderItems) != 1 || c.ModelID != p.Model {
			t.Error("committed the wrong source")
		}
		return nil
	}
	a.Run(t.Context(), history, "system", func(e AgentEvent) { events = append(events, e) }, nil, "", nil)
	if a.RunFailure() != nil || commits != 1 || paid.Load() != 1 || normal.Load() != 2 || !hasEvent(events, "compaction_route_notice") {
		t.Fatalf("failure=%v commits=%d native=%d normal=%d", a.RunFailure(), commits, paid.Load(), normal.Load())
	}
}

// In Auto an uncertain native result must not stop the run or resend the
// paid request: Errand writes the full checkpoint and the user is told once.
func TestAutoUncertainNativeCheckpointUsesErrandAndContinues(t *testing.T) {
	var paid, mainCalls atomic.Int32
	var ticket atomic.Value
	const old = "old assistant evidence "
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		s := string(raw)
		var body struct {
			Instructions string
			Stream       bool
		}
		_ = json.Unmarshal(raw, &body)
		if strings.HasSuffix(r.URL.Path, "/compact") {
			if strings.Contains(s, old) {
				paid.Add(1)
				http.Error(w, `{"error":{"message":"upstream timed out"}}`, http.StatusBadGateway)
				return
			}
			if parts := strings.Fields(body.Instructions); len(parts) > 4 {
				ticket.Store(parts[4])
			}
			fmt.Fprint(w, `{"object":"response.compaction","output":[{"type":"compaction","encrypted_content":"probe-token"}]}`)
			return
		}
		if !body.Stream {
			answer, _ := ticket.Load().(string)
			fmt.Fprintf(w, `{"object":"response","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":%q}]}]}`, answer)
			return
		}
		mainCalls.Add(1)
		if strings.Contains(s, old) || !strings.Contains(s, "context_checkpoint") {
			t.Error("main request did not use the portable checkpoint")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}]}}`+"\n\n")
	}))
	defer gateway.Close()
	recorder := &precompactionServer{}
	summary := httptest.NewServer(recorder.handler(t))
	defer summary.Close()
	p := &provider.Provider{ID: "custom", AccountID: "auto-uncertain", StandardName: "openai-responses", Model: "auto-native", BaseURL: gateway.URL + "/v1"}
	p.SetContextWindow(100_000)
	a := newTestAgent(t, p, p.Model)
	a.CompactionProvider = provider.FromPreset("openai-chat", "key", summary.URL)
	a.CompactionProvider.Model = "extractor-test"
	a.CompactionProvider.SetContextWindow(200_000)
	a.ContextCompactionPercent = 10
	var history []provider.ChatMessage
	for i := range 14 {
		history = append(history, provider.NewTextMessage("user", fmt.Sprintf("step %d", i)), provider.NewTextMessage("assistant", old+strings.Repeat("payload ", 1400)))
	}
	history = append(history, provider.NewTextMessage("user", "Continue the check."))
	var events []AgentEvent
	a.Run(t.Context(), history, "system", func(e AgentEvent) { events = append(events, e) }, nil, "", nil)
	var reason string
	for _, e := range events {
		if e.Type == "compaction_route_notice" {
			reason, _ = e.Data.(map[string]any)["reason"].(string)
		}
	}
	if a.RunFailure() != nil || paid.Load() != 1 || recorder.compactions.Load() != 1 || mainCalls.Load() != 1 || hasEvent(events, "error") || !hasEvent(events, "context_compacted") {
		t.Fatalf("failure=%v native=%d errand=%d main=%d", a.RunFailure(), paid.Load(), recorder.compactions.Load(), mainCalls.Load())
	}
	if reason != "The provider did not confirm native compaction (HTTP 502). Using Errand." {
		t.Fatalf("notice %q", reason)
	}
}

// Wires without a native compaction operation still have the serving
// model. Provider only never charges another model, Auto uses the serving
// model when its cached context is cheaper or Errand cannot run, and Errand
// only always uses Errand. A serving checkpoint declares the conversation's
// tools (forbidden), so it reads the conversation's cached prefix.
func TestServingModelWritesCheckpointOnWiresWithoutNativeCompaction(t *testing.T) {
	const handoff = "<<<intent>>>\nKeep the build passing\n<<<work>>>\nChecked inputs\n<<<state>>>\nTarget is blue\n<<<continuation>>>\nCheck the target"
	type route struct {
		calls  atomic.Int32
		bodies []map[string]any
		mu     sync.Mutex
	}
	serve := func(r *route) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			var body map[string]any
			_ = json.NewDecoder(req.Body).Decode(&body)
			r.mu.Lock()
			r.bodies = append(r.bodies, body)
			r.mu.Unlock()
			r.calls.Add(1)
			writeChatSSE(w, handoff, "stop")
		}))
	}
	provider.SetModelPrice("serving-cheap-cached", provider.ModelPrice{InputPricePerToken: 3e-6, OutputPricePerToken: 1.5e-5, InputCachedPricePerToken: 3e-7})
	provider.SetModelPrice("errand-flat", provider.ModelPrice{InputPricePerToken: 1e-6, OutputPricePerToken: 4e-6})
	provider.SetModelPrice("errand-cheap", provider.ModelPrice{InputPricePerToken: 1e-7, OutputPricePerToken: 4e-7})
	tools := []provider.ToolDef{{Type: "function", Function: provider.FunctionDef{Name: "read_file", Description: "Read a file.", Parameters: json.RawMessage(`{"type":"object"}`)}}}
	for _, test := range []struct {
		name, method, errandModel string
		ratio                     float64
		broken                    bool
		serving                   bool
	}{
		{name: "provider only", method: provider.CompactionProviderOnly, errandModel: "errand-cheap", serving: true},
		{name: "auto cached serving is cheaper", errandModel: "errand-flat", ratio: 0.95, serving: true},
		{name: "auto errand is cheaper", errandModel: "errand-cheap", ratio: 0.95},
		{name: "auto errand cannot run", errandModel: "errand-cheap", broken: true, serving: true},
		{name: "errand only", method: provider.CompactionErrandOnly, errandModel: "errand-flat", ratio: 0.95},
	} {
		t.Run(test.name, func(t *testing.T) {
			var main, errand route
			mainServer, errandServer := serve(&main), serve(&errand)
			defer mainServer.Close()
			defer errandServer.Close()
			serving := provider.FromPreset("openai-chat", "key", mainServer.URL)
			serving.Model, serving.AccountID = "serving-cheap-cached", "serving-"+test.name
			serving.SetContextWindow(1_000_000)
			if test.method != "" {
				serving.SetProtocolSettings(&provider.ProtocolSettings{Compaction: test.method})
			}
			extractor := provider.FromPreset("openai-chat", "key", errandServer.URL)
			extractor.Model, extractor.AccountID = test.errandModel, "errand-"+test.name
			extractor.SetContextWindow(200_000)
			a := newTestAgent(t, extractor, extractor.Model)
			a.NativeCompactionProvider = serving
			a.CompactionTargetModel, a.CompactionTargetWindow = serving.Model, 1_000_000
			a.servingCacheRatio = test.ratio
			a.FilteredTools = nil
			if test.broken {
				a.CompactionConfigError = errors.New("Errand has no credentials")
			}
			history := []provider.ChatMessage{provider.NewTextMessage("user", "Review the build "+strings.Repeat("evidence ", 3000))}
			var events []AgentEvent
			msgs := append([]provider.ChatMessage{provider.NewTextMessage("system", "system")}, history...)
			// The decision uses the source size the run measured: 400K tokens.
			outcome, err := a.compactWithModel(t.Context(), msgs, tools, "op", 400_000, CompactionTriggerManual, func(e AgentEvent) { events = append(events, e) })
			if err != nil {
				t.Fatal(err)
			}
			if test.serving != (main.calls.Load() > 0) || test.serving == (errand.calls.Load() > 0) || outcome.ModelID != map[bool]string{true: serving.Model, false: extractor.Model}[test.serving] {
				t.Fatalf("main=%d errand=%d model=%s", main.calls.Load(), errand.calls.Load(), outcome.ModelID)
			}
			if test.serving {
				body := main.bodies[0]
				if body["tool_choice"] != "none" || body["tools"] == nil {
					t.Fatalf("serving checkpoint lost the cached tool prefix: %v", body)
				}
			} else if errand.bodies[0]["tools"] != nil {
				t.Fatal("a different compaction model received the conversation's tools")
			}
			if test.broken != hasEvent(events, "compaction_route_notice") {
				t.Fatalf("notice for broken Errand = %v", hasEvent(events, "compaction_route_notice"))
			}
		})
	}
}
