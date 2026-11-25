package turnloop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

	"crude/agent/provider"
	"crude/internal/providersim"
)

// A human request compacts before the next model request even when
// automatic compaction is off and the context is far from any threshold.
func TestRunHonoursHumanCompactionRequestWithAutomaticCompactionOff(t *testing.T) {
	server := providersim.New(t)
	server.Respond(func(r providersim.Request) providersim.Turn {
		if r.Index <= len(compactionStagesForTest) {
			return providersim.Text("- compact stage: preserved facts").WithUsage(providersim.Usage{Input: 1_000, Output: 10})
		}
		return providersim.Text("done").WithUsage(providersim.Usage{Input: 1_000, Output: 1})
	})
	p := provider.FromPreset("ollama-cloud", "ollama-key", server.BaseURL(providersim.Ollama))
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
		t.Fatalf("requested compaction did not run: %+v (requests=%d)", compacted, len(server.Requests()))
	}
	last, ok := server.Last()
	if !ok {
		t.Fatal("no provider request")
	}
	final := conversationText(last.Conversation)
	if strings.Contains(final, "answer 5 ") || !strings.Contains(final, "context_checkpoint") || strings.Contains(final, "request 0 ") || !strings.Contains(final, "current instruction must survive") {
		t.Fatal("the next model request did not use the compacted context")
	}
}

// A compaction choice the user makes while a run works applies at the next
// step. The run starts with automatic compaction off; the owner's current
// settings ask for 5%, so the step compacts and adopts the other passive
// settings without waiting for another run.
func TestRunAppliesLiveSettingsBeforeEachStep(t *testing.T) {
	server := providersim.New(t)
	server.Respond(func(r providersim.Request) providersim.Turn {
		if r.Index <= len(compactionStagesForTest) {
			return providersim.Text("- compact stage: preserved facts").WithUsage(providersim.Usage{Input: 1_000, Output: 10})
		}
		return providersim.Text("done").WithUsage(providersim.Usage{Input: 1_000, Output: 1})
	})
	p := provider.FromPreset("ollama-cloud", "ollama-key", server.BaseURL(providersim.Ollama))
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
		t.Fatalf("the live 5%% choice did not compact: %+v (requests=%d)", compacted, len(server.Requests()))
	}
}

// The wire is Responses, the main model is Claude, and the summarizer uses a
// separate Chat connection. Only synthetic input may reach discovery.
func TestCustomResponsesColdCheckpoint(t *testing.T) {
	var mainCalls atomic.Int32
	const old = "long historical assistant output: "
	const instruction = "Continue checking the unresolved build error."
	gateway := providersim.New(t)
	gateway.Respond(func(r providersim.Request) providersim.Turn {
		var wire struct{ Input json.RawMessage }
		if err := json.Unmarshal(r.Raw, &wire); err != nil {
			t.Error(err)
		}
		sent := string(wire.Input)
		if strings.HasSuffix(r.Path, "/compact") {
			if strings.Contains(string(r.Raw), old) || strings.Contains(string(r.Raw), instruction) {
				t.Error("probe received real history")
			}
			return providersim.HTTPError(providersim.ModelNotFound)
		}
		for _, m := range r.Messages {
			for _, p := range m.Parts {
				if p.Kind != providersim.PartOpaque {
					continue
				}
				var item struct{ Type string }
				if err := json.Unmarshal(p.Raw, &item); err != nil {
					t.Error(err)
				}
				if item.Type == "compaction_trigger" {
					if strings.Contains(sent, old) || strings.Contains(sent, instruction) {
						t.Error("trigger probe exposed conversation")
					}
					return providersim.HTTPError(providersim.InvalidRequest).WithFaultMessage("unsupported compaction trigger")
				}
			}
		}
		mainCalls.Add(1)
		if r.Path != "/v1/responses" || r.Model != "claude-gateway-test" || r.PreviousResponseID != "" || !strings.Contains(sent, "context_checkpoint") || !strings.Contains(sent, instruction) || strings.Contains(sent, old) {
			t.Errorf("unexpected cold request: path=%s model=%s anchor=%s", r.Path, r.Model, r.PreviousResponseID)
		}
		return providersim.Text("done")
	})
	// A real tool-free extraction endpoint, not an injected compact outcome.
	recorder := &precompactionServer{}
	summary := recorder.start(t)
	p := &provider.Provider{ID: "custom", StandardName: "openai-responses", Model: "claude-gateway-test", BaseURL: gateway.BaseURL(providersim.Responses)}
	p.SetContextWindow(100_000)
	a := newTestAgent(t, p, p.Model)
	a.CompactionProvider = provider.FromPreset("openai-chat", "key", summary.BaseURL(providersim.Chat))
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
	const handoff = "<<<intent>>>\nKeep the build passing\n<<<work>>>\nChecked inputs\n<<<state>>>\nTarget is blue\n<<<continuation>>>\nCheck the target"
	server := providersim.New(t)
	server.Respond(func(r providersim.Request) providersim.Turn {
		sent, visible := conversationText(r.Conversation), conversationText(r.Effective)
		if r.Model != "fixture-native" {
			t.Error("provider-only switched models")
		}
		if strings.HasSuffix(r.Path, "/compact") {
			if strings.Contains(sent, "old assistant evidence") {
				paid.Add(1)
			} else if probeTicket(sent) == "" {
				t.Error("missing probe code")
			}
			return providersim.Turn{}
		}
		if strings.Contains(sent, "old assistant evidence") {
			t.Error("old history replayed")
		}
		if isReadBack(sent) {
			if code := probeTicket(visible); code != "" {
				return providersim.Text(code)
			}
			return providersim.Text(handoff)
		}
		if normal.Add(1) == 1 {
			if !hasSimulatedCheckpoint(r.Conversation) || !strings.Contains(visible, "old assistant evidence") {
				t.Error("missing native window on main request")
			}
			return providersim.HTTPError(providersim.InvalidRequest).WithFaultMessage("invalid compaction item")
		}
		if hasSimulatedCheckpoint(r.Conversation) || !strings.Contains(sent, "context_checkpoint") || !strings.Contains(sent, "Target is blue") {
			t.Error("main retry did not use portable state")
		}
		return providersim.Text("done")
	})
	p := &provider.Provider{ID: "custom", AccountID: "native-e2e", StandardName: "openai-responses", Model: "fixture-native", BaseURL: server.BaseURL(providersim.Responses)}
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
	const old = "old assistant evidence "
	gateway := providersim.New(t)
	gateway.Respond(func(r providersim.Request) providersim.Turn {
		sent := conversationText(r.Conversation)
		if strings.HasSuffix(r.Path, "/compact") {
			if strings.Contains(sent, old) {
				paid.Add(1)
				return providersim.HTTPError(providersim.BadGateway).WithFaultMessage("upstream timed out")
			}
			return providersim.Turn{}
		}
		if isReadBack(sent) {
			return providersim.Text(probeTicket(conversationText(r.Effective)))
		}
		mainCalls.Add(1)
		if strings.Contains(sent, old) || !strings.Contains(sent, "context_checkpoint") {
			t.Error("main request did not use the portable checkpoint")
		}
		return providersim.Text("done")
	})
	recorder := &precompactionServer{}
	summary := recorder.start(t)
	p := &provider.Provider{ID: "custom", AccountID: "auto-uncertain", StandardName: "openai-responses", Model: "auto-native", BaseURL: gateway.BaseURL(providersim.Responses)}
	p.SetContextWindow(100_000)
	a := newTestAgent(t, p, p.Model)
	a.CompactionProvider = provider.FromPreset("openai-chat", "key", summary.BaseURL(providersim.Chat))
	a.CompactionProvider.Model = "extractor-test"
	a.CompactionProvider.SetContextWindow(200_000)
	a.ContextCompactionPercent = 10
	var history []provider.ChatMessage
	for i := range 14 {
		history = append(history, provider.NewTextMessage("user", fmt.Sprintf("step %d", i)), provider.NewTextMessage("assistant", old+strings.Repeat("payload ", 1400)))
	}
	history = append(servedBy(p, history), provider.NewTextMessage("user", "Continue the check."))
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
// model. For automatic checkpoints Provider only never charges another model,
// Auto uses the serving model when it is cheaper or Errand cannot run, and
// Errand only always uses Errand. A serving checkpoint declares the
// conversation's tools (forbidden), so it reads the conversation's cached
// prefix; a context another model wrote has none, so the comparison counts no
// cached input for it. The user's /compact is Errand's with every setting;
// the serving model writes it only when Errand cannot run.
func TestServingModelWritesCheckpointOnWiresWithoutNativeCompaction(t *testing.T) {
	const handoff = "<<<intent>>>\nKeep the build passing\n<<<work>>>\nChecked inputs\n<<<state>>>\nTarget is blue\n<<<continuation>>>\nCheck the target"
	serve := func(t *testing.T) *providersim.Server {
		s := providersim.New(t)
		s.Respond(func(providersim.Request) providersim.Turn { return providersim.Text(handoff) })
		return s
	}
	provider.SetModelPrice("serving-cheap-cached", provider.ModelPrice{InputPricePerToken: 3e-6, OutputPricePerToken: 1.5e-5, InputCachedPricePerToken: 3e-7})
	provider.SetModelPrice("errand-flat", provider.ModelPrice{InputPricePerToken: 1e-6, OutputPricePerToken: 4e-6})
	provider.SetModelPrice("errand-cheap", provider.ModelPrice{InputPricePerToken: 1e-7, OutputPricePerToken: 4e-7})
	provider.SetModelPrice("errand-costly", provider.ModelPrice{InputPricePerToken: 5e-6, OutputPricePerToken: 2e-5})
	tools := []provider.ToolDef{{Type: "function", Function: provider.FunctionDef{Name: "read_file", Description: "Read a file.", Parameters: json.RawMessage(`{"type":"object"}`)}}}
	for _, test := range []struct {
		name, method, errandModel string
		trigger, writtenBy        string
		ratio                     float64
		broken                    bool
		serving                   bool
	}{
		{name: "provider only", method: provider.CompactionProviderOnly, errandModel: "errand-cheap", serving: true},
		{name: "provider only human request", method: provider.CompactionProviderOnly, errandModel: "errand-cheap", trigger: CompactionTriggerManual},
		{name: "provider only human request errand cannot run", method: provider.CompactionProviderOnly, errandModel: "errand-cheap", trigger: CompactionTriggerManual, broken: true, serving: true},
		{name: "auto cached serving is cheaper", errandModel: "errand-flat", ratio: 0.95, serving: true},
		{name: "auto human request", errandModel: "errand-flat", ratio: 0.95, trigger: CompactionTriggerManual},
		{name: "auto in-run human request", errandModel: "errand-flat", ratio: 0.95, trigger: "requested"},
		{name: "auto after a model change", errandModel: "errand-flat", ratio: 0.95, writtenBy: "previous-model"},
		{name: "auto human request after a model change", errandModel: "errand-flat", ratio: 0.95, trigger: CompactionTriggerManual, writtenBy: "previous-model"},
		{name: "auto after a model change uncached serving is cheaper", errandModel: "errand-costly", ratio: 0.95, writtenBy: "previous-model", serving: true},
		{name: "auto errand is cheaper", errandModel: "errand-cheap", ratio: 0.95},
		{name: "auto errand cannot run", errandModel: "errand-cheap", broken: true, serving: true},
		{name: "auto human request errand cannot run", errandModel: "errand-cheap", trigger: CompactionTriggerManual, broken: true, serving: true},
		{name: "errand only", method: provider.CompactionErrandOnly, errandModel: "errand-flat", ratio: 0.95},
	} {
		t.Run(test.name, func(t *testing.T) {
			main, errand := serve(t), serve(t)
			serving := provider.FromPreset("openai-chat", "key", main.BaseURL(providersim.Chat))
			serving.Model, serving.AccountID = "serving-cheap-cached", "serving-"+test.name
			serving.SetContextWindow(1_000_000)
			if test.method != "" {
				serving.SetProtocolSettings(&provider.ProtocolSettings{Compaction: test.method})
			}
			extractor := provider.FromPreset("openai-chat", "key", errand.BaseURL(providersim.Chat))
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
			history := []provider.ChatMessage{
				provider.NewTextMessage("user", "Review the build "+strings.Repeat("evidence ", 3000)),
				provider.NewTextMessage("assistant", "The build is under review."),
				provider.NewTextMessage("user", "Continue."),
			}
			trigger := test.trigger
			if trigger == "" {
				trigger = "automatic"
			}
			var events []AgentEvent
			msgs := append([]provider.ChatMessage{provider.NewTextMessage("system", "system")}, servedBy(serving, history)...)
			if test.writtenBy != "" {
				// Same connection, another model: the user switched models.
				msgs[2].ReplaySourceModel = test.writtenBy
			}
			// The decision uses the source size the run measured: 400K tokens.
			outcome, err := a.compactWithModel(t.Context(), msgs, tools, "op", 400_000, trigger, func(e AgentEvent) { events = append(events, e) })
			if err != nil {
				t.Fatal(err)
			}
			if test.serving != (len(main.Requests()) > 0) || test.serving == (len(errand.Requests()) > 0) || outcome.ModelID != map[bool]string{true: serving.Model, false: extractor.Model}[test.serving] {
				t.Fatalf("main=%d errand=%d model=%s", len(main.Requests()), len(errand.Requests()), outcome.ModelID)
			}
			if test.serving {
				body := main.Requests()[0]
				if string(body.ToolChoice) != `"none"` || len(body.Tools) == 0 {
					t.Fatalf("serving checkpoint lost the cached tool prefix: %v", body)
				}
			} else if errand.Requests()[0].Tools != nil {
				t.Fatal("a different compaction model received the conversation's tools")
			}
			if test.broken != hasEvent(events, "compaction_route_notice") {
				t.Fatalf("notice for broken Errand = %v", hasEvent(events, "compaction_route_notice"))
			}
		})
	}
}

var probeTicketPattern = regexp.MustCompile(`ticket code (\S+) exactly`)

// probeTicket reads the synthetic challenge wherever the wire carries it.
func probeTicket(body string) string {
	if match := probeTicketPattern.FindStringSubmatch(body); match != nil {
		return match[1]
	}
	return ""
}

// isReadBack reports a request that reads a native window back: the synthetic
// check's question or the checkpoint instructions. Both may stream.
func isReadBack(body string) bool {
	return strings.Contains(body, "What was the ticket code?") || statedCheckpointSpace.MatchString(body)
}

func hasSimulatedCheckpoint(c providersim.Conversation) bool {
	for _, message := range c.Messages {
		for _, part := range message.Parts {
			if part.Kind == providersim.PartOpaque {
				var item struct{ Type string }
				if json.Unmarshal(part.Raw, &item) == nil && item.Type == "compaction" {
					return true
				}
			}
		}
	}
	return false
}

// On 2026-11-25 a user switched a session from one model to another on the
// same Responses gateway, stopped the turn and compacted. Crude sent the
// whole history (491,798 input tokens, none cached) to the new model's native
// compaction. A model that did not write the context has none of it in cache,
// so the provider's compaction is not tried and Errand writes the checkpoint.
// An automatic checkpoint of a context the serving model wrote may use its
// native route. The user's /compact is always Errand's.
func TestCheckpointAfterModelChangeUsesErrand(t *testing.T) {
	for _, test := range []struct {
		name, writtenBy string
		human, native   bool
	}{
		{name: "automatic after a model change", writtenBy: "previous-model"},
		{name: "human request after a model change", writtenBy: "previous-model", human: true},
		{name: "human request on the same model", writtenBy: "current-model", human: true},
		{name: "automatic on the same model", writtenBy: "current-model", native: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			const old = "old assistant evidence "
			var native, mainCalls atomic.Int32
			gateway := providersim.New(t)
			gateway.Respond(func(r providersim.Request) providersim.Turn {
				if strings.HasSuffix(r.Path, "/compact") || strings.Contains(string(r.Raw), "compaction_trigger") {
					native.Add(1)
					return providersim.HTTPError(providersim.InvalidRequest).WithFaultMessage("unsupported compaction trigger")
				}
				mainCalls.Add(1)
				if sent := conversationText(r.Conversation); strings.Contains(sent, old) || !strings.Contains(sent, "context_checkpoint") {
					t.Error("the serving model received the old history instead of the checkpoint")
				}
				return providersim.Text("done")
			})
			recorder := &precompactionServer{}
			summary := recorder.start(t)
			p := &provider.Provider{ID: "custom", AccountID: "model-change-" + test.name, StandardName: "openai-responses", Model: "current-model", BaseURL: gateway.BaseURL(providersim.Responses)}
			p.SetContextWindow(100_000)
			a := newTestAgent(t, p, p.Model)
			a.CompactionProvider = provider.FromPreset("openai-chat", "key", summary.BaseURL(providersim.Chat))
			a.CompactionProvider.Model = "extractor-test"
			a.CompactionProvider.SetContextWindow(200_000)
			a.ContextCompactionPercent = 10
			if test.human {
				a.ContextCompactionPercent = CompactionOff
				taken := false
				a.CompactionRequest = func() string {
					if taken {
						return ""
					}
					taken = true
					return "human-request"
				}
			}
			var history []provider.ChatMessage
			for i := range 14 {
				history = append(history, provider.NewTextMessage("user", fmt.Sprintf("step %d", i)), provider.NewTextMessage("assistant", old+strings.Repeat("payload ", 1400)))
			}
			history = servedBy(p, history)
			for i := range history {
				if history[i].Role == "assistant" {
					history[i].ReplaySourceModel = test.writtenBy
				}
			}
			history = append(history, provider.NewTextMessage("user", "Continue the check."))
			var commits []CompactionCommit
			a.CommitCompaction = func(c CompactionCommit) error { commits = append(commits, c); return nil }
			a.Run(t.Context(), history, "system", func(AgentEvent) {}, nil, "", nil)
			if a.RunFailure() != nil || len(commits) != 1 || mainCalls.Load() != 1 {
				t.Fatalf("failure=%v commits=%d main=%d", a.RunFailure(), len(commits), mainCalls.Load())
			}
			if test.native != (native.Load() > 0) {
				t.Fatalf("native requests=%d; want them only for a context the serving model wrote", native.Load())
			}
			if recorder.compactions.Load() != 1 || commits[0].ModelID != "extractor-test" {
				t.Fatalf("errand extractions=%d writer=%q; want Errand to write the checkpoint", recorder.compactions.Load(), commits[0].ModelID)
			}
		})
	}
}
