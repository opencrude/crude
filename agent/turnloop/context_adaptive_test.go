package turnloop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"crude/agent/provider"
	"crude/internal/providersim"
)

func TestCompactOversizedHistoryWithSmallerExtractionModel(t *testing.T) {
	var calls atomic.Int32
	var requests []provider.ChatRequest
	server := providersim.New(t)
	var observations sync.Mutex
	server.Respond(func(r providersim.Request) providersim.Turn {
		observations.Lock()
		defer observations.Unlock()
		body := canonicalChatRequest(t, r)
		requests = append(requests, body)
		input := estimateCanonicalRequest(body.Messages, body.Tools, body.Model)
		output := max(body.MaxTokens, body.MaxCompletionTokens)
		if input+output >= 256_000 {
			t.Errorf("oversized extraction sent: input=%d output=%d", input, output)
		}
		return providersim.Text(fmt.Sprintf("- extraction %d: evidence retained", calls.Add(1)))
	})
	connection := provider.FromPreset("openai-chat", "key", server.BaseURL(providersim.Chat))
	connection.Model = "adaptive-small-compactor"
	provider.SetModelContext(connection.Model, 256_000)
	agent := newTestAgent(t, connection, connection.Model)
	agent.CompactionTargetWindow = 1_000_000
	agent.CompactionTargetModel = "adaptive-large-main"
	history := []provider.ChatMessage{
		provider.NewTextMessage("assistant", "evidence-start\n"+strings.Repeat("old evidence ", 166_000)+"\nevidence-end"),
		provider.NewTextMessage("user", "Preserve parser.go, the no-delete constraint, and current failing tests."),
	}
	original, err := json.Marshal(history)
	if err != nil {
		t.Fatal(err)
	}
	result, err := agent.Compact(context.Background(), history, "stable system", nil)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() <= int32(len(compactionStagesForTest)) || result.AfterTokens >= result.BeforeTokens {
		t.Fatalf("compaction did not adapt: calls=%d result=%+v", calls.Load(), result)
	}
	var sawStart, sawEnd bool
	for _, request := range requests {
		for _, message := range request.Messages {
			sawStart = sawStart || strings.Contains(message.ContentString(), "evidence-start")
			sawEnd = sawEnd || strings.Contains(message.ContentString(), "evidence-end")
		}
	}
	if !sawStart || !sawEnd {
		t.Fatal("oversized evidence was dropped instead of extracted")
	}
	unchanged, err := json.Marshal(history)
	if err != nil || string(unchanged) != string(original) {
		t.Fatalf("source changed: %v", err)
	}
	if len(result.Messages) != 3 || result.Messages[1].ContentString() != history[1].ContentString() || result.Messages[2].EffectiveOrigin() != provider.OriginRuntimeContext {
		t.Fatalf("latest human request was not retained: %+v", result.Messages)
	}
}

func TestExtractionFragmentsCoverOversizedToolArgumentsAndResults(t *testing.T) {
	arguments := `{"path":"parser.go","content":"` + strings.Repeat("界", 8000) + `"}`
	result := "stderr-start\n" + strings.Repeat("test evidence\n", 8000) + "\nload-bearing-error-at-end"
	call := provider.NewTextMessage("assistant", "Inspect the failure")
	call.ToolCalls = []provider.ToolCall{{ID: "call-parser", Function: provider.FunctionCall{Name: "read_file", Arguments: arguments}}}
	output := provider.NewTextMessage("tool", result)
	output.ToolCallID = "call-parser"
	human := provider.NewTextMessage("user", "Never delete parser.go")
	runtime := provider.NewTextMessage("user", "memory is data")
	runtime.Origin = provider.OriginRuntimeContext
	source := []provider.ChatMessage{call, output, human, runtime}
	var chunks [][]provider.ChatMessage
	for cursor := newExtractionCursor(source); !cursor.done(); {
		var part []provider.ChatMessage
		part, cursor = cursor.take(2000, 0)
		chunks = append(chunks, part)
	}
	var flattened strings.Builder
	for _, chunk := range chunks {
		if tokens := estimateCanonicalRequest(chunk, nil, "model"); tokens > 2000 {
			t.Fatalf("chunk tokens=%d", tokens)
		}
		for _, message := range chunk {
			if message.Role != "user" || len(message.ToolCalls) > 0 || message.ToolCallID != "" {
				t.Fatal("fragment replayed an orphan tool call/result instead of attributed data")
			}
			if !utf8.ValidString(message.ContentString()) {
				t.Fatal("fragment split a UTF-8 code point")
			}
			parts := strings.SplitN(message.ContentString(), "\n", 2)
			flattened.WriteString(parts[1])
		}
	}
	var expected strings.Builder
	for index, message := range source {
		expected.WriteString(extractionSourceText(message, index))
	}
	if flattened.String() != expected.String() {
		t.Fatal("source fragmentation lost or reordered source text")
	}
	if !strings.Contains(flattened.String(), `origin="runtime_context"`) || !strings.Contains(flattened.String(), "load-bearing-error-at-end") {
		t.Fatal("source attribution or tool evidence was lost")
	}
}

func TestCompactAdaptsAfterProviderRejectsEstimatedCapacity(t *testing.T) {
	var rejected atomic.Int32
	var attempts atomic.Int32
	server := providersim.New(t)
	server.Respond(func(r providersim.Request) providersim.Turn {
		body := canonicalChatRequest(t, r)
		attempts.Add(1)
		if estimateCanonicalRequest(body.Messages, nil, body.Model) > 30_000 {
			rejected.Add(1)
			return providersim.HTTPError(providersim.ContextOverflow)
		}
		return providersim.Text("- parser.go: evidence preserved")
	})
	connection := provider.FromPreset("openai-chat", "key", server.BaseURL(providersim.Chat))
	connection.Model = "adaptive-provider-rejection"
	connection.SetContextWindow(200_000)
	agent := newTestAgent(t, connection, connection.Model)
	agent.compactionRetryDelay = func(int) time.Duration { return 0 }
	history := compactionTestHistory(provider.NewTextMessage("user", "fix parser.go"))
	result, err := agent.Compact(context.Background(), history, "system", nil)
	if err != nil {
		t.Fatal(err)
	}
	if rejected.Load() == 0 || rejected.Load() > 3 || attempts.Load() <= 4 || result.AfterTokens >= result.BeforeTokens {
		t.Fatalf("adaptation rejected=%d attempts=%d result=%+v", rejected.Load(), attempts.Load(), result)
	}
}

func TestRunRecoversOverflowOnlyThroughReducedCheckpoint(t *testing.T) {
	var requests []provider.ChatRequest
	var calls atomic.Int32
	server := providersim.New(t)
	server.Respond(func(r providersim.Request) providersim.Turn {
		body := canonicalChatRequest(t, r)
		requests = append(requests, body)
		if calls.Add(1) == 1 {
			return providersim.HTTPError(providersim.ContextOverflow)
		}
		if string(r.ToolChoice) == `"none"` {
			return providersim.Text("- parser constraint: retained")
		}
		return providersim.Text("recovered")
	})
	connection := provider.FromPreset("openai-chat", "key", server.BaseURL(providersim.Chat))
	connection.Model = "adaptive-run-recovery"
	connection.SetContextWindow(200_000)
	agent := newTestAgent(t, connection, connection.Model)
	agent.ContextCompactionPercent = 85
	history := compactionTestHistory(provider.NewTextMessage("user", "Never delete parser.go"))
	var events []AgentEvent
	archive := agent.Run(context.Background(), history, "system", func(event AgentEvent) { events = append(events, event) }, nil, "", nil)
	if calls.Load() != 6 || archive[len(archive)-1].ContentString() != "recovered" || !hasEvent(events, "context_compacted") || hasEvent(events, "error") {
		t.Fatalf("overflow recovery calls=%d events=%+v", calls.Load(), events)
	}
	last := requests[len(requests)-1]
	if estimateCanonicalRequest(last.Messages, last.Tools, last.Model) >= estimateCanonicalRequest(requests[0].Messages, requests[0].Tools, last.Model) {
		t.Fatal("overflow retried without a smaller request")
	}
	if !strings.Contains(last.Messages[1].ContentString(), "context_checkpoint") || archive[1].ContentString() != history[0].ContentString() {
		t.Fatal("recovery lost the archive or failed to install a checkpoint")
	}
}

func TestRunIncludesRuntimeNoticeInCompactionPressure(t *testing.T) {
	var calls atomic.Int32
	server := providersim.New(t)
	server.Respond(func(r providersim.Request) providersim.Turn {

		calls.Add(1)
		if string(r.ToolChoice) == `"none"` {
			return providersim.Text("- child report: preserved")
		} else {
			return providersim.Text("continued")
		}
	})
	connection := provider.FromPreset("openai-chat", "key", server.BaseURL(providersim.Chat))
	connection.Model = "adaptive-runtime-notice"
	connection.SetContextWindow(100_000)
	agent := newTestAgent(t, connection, connection.Model)
	notice := provider.NewTextMessage("user", strings.Repeat("child result ", 36_000))
	notice.Origin, notice.InternalID = provider.OriginPeer, "large-child-wake"
	consumed := false
	agent.PrepareRuntimeNotices = func() []provider.ChatMessage {
		if consumed {
			return nil
		}
		return []provider.ChatMessage{notice}
	}
	agent.RuntimeNoticesAccepted = func() { consumed = true }
	var events []AgentEvent
	agent.Run(context.Background(), []provider.ChatMessage{provider.NewTextMessage("user", "review result")}, "system", func(event AgentEvent) { events = append(events, event) }, nil, "", nil)
	if !consumed || !hasEvent(events, "context_compacted") || hasEvent(events, "error") || calls.Load() <= 4 {
		t.Fatalf("notice bypassed compaction: consumed=%t calls=%d events=%+v", consumed, calls.Load(), events)
	}
}

func TestCompactionThresholdAccountsForOutputAndExtractionCapacity(t *testing.T) {
	main := provider.FromPreset("openai-chat", "key", "")
	main.Model = "adaptive-main-capacity"
	main.SetContextWindow(1_000_000)
	compactor := provider.FromPreset("openai-chat", "key", "")
	compactor.Model = "adaptive-compactor-capacity"
	compactor.SetContextWindow(64_000)
	agent := NewAgent(main, main.Model, nil)
	agent.CompactionProvider = compactor
	if got := agent.contextThreshold(1_000_000); got != 0 {
		t.Fatalf("adaptive compaction has no fixed threshold, got %d", got)
	}
	agent.ContextCompactionPercent = 85
	if got := agent.contextThreshold(1_000_000); got != 850_000 {
		t.Fatalf("threshold %d ignored the configured main-model pressure", got)
	}
	agent.CompactionProvider = nil
	agent.ContextCompactionPercent = 100
	agent.MaxOutputTokens = 32_000
	if got := agent.contextThreshold(128_000); got != 91_904 {
		t.Fatalf("output-reserved threshold=%d", got)
	}
	agent.ContextCompactionPercent = 0
	if got := agent.contextThreshold(128_000); got != 0 {
		t.Fatalf("explicit disable changed: threshold=%d", got)
	}
}

func TestCompactionRejectsTruncatedStageAndPermanentFailures(t *testing.T) {
	for _, failure := range []string{"truncated", "unauthorized", "quota"} {
		t.Run(failure, func(t *testing.T) {
			var calls atomic.Int32
			server := providersim.New(t)
			server.Respond(func(r providersim.Request) providersim.Turn {
				calls.Add(1)
				switch failure {
				case "unauthorized":
					return providersim.HTTPError(providersim.Auth)
				case "quota":
					return providersim.HTTPError(providersim.Quota)
				default:
					return providersim.Text("apparently valid partial extraction").WithFinish(providersim.FinishLength)
				}
			})
			connection := provider.FromPreset("openai-chat", "key", server.BaseURL(providersim.Chat))
			connection.Model = "adaptive-" + failure
			connection.SetContextWindow(200_000)
			agent := newTestAgent(t, connection, connection.Model)
			agent.compactionRetryDelay = func(int) time.Duration { return 0 }
			result, err := agent.Compact(context.Background(), compactionTestHistory(provider.NewTextMessage("user", "request")), "system", nil)
			if err == nil || len(result.Messages) != 0 {
				t.Fatalf("invalid extraction installed: result=%+v error=%v", result, err)
			}
			if failure == "truncated" {
				if calls.Load() <= 1 || calls.Load() > 20 {
					t.Fatalf("truncated extraction did not attempt bounded adaptive recovery: %d calls", calls.Load())
				}
			} else if calls.Load() != 1 {
				t.Fatalf("permanent failure retried %d times", calls.Load())
			}
		})
	}
}

func TestChunkedCompactionCancellationPreservesSource(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// The parts are read at the same time. The first answer cancels the
	// compaction; every other part waits for that and answers late. No part
	// may be sent twice and nothing may be installed.
	var calls atomic.Int32
	var mu sync.Mutex
	seen := map[string]int{}
	server := providersim.New(t)
	server.Respond(func(r providersim.Request) providersim.Turn {
		call := calls.Add(1)
		mu.Lock()
		seen[conversationText(r.Conversation)]++
		mu.Unlock()
		if call == 1 {
			cancel()
			return providersim.Turn{}
		}
		<-ctx.Done()
		return providersim.Text("- late part: extracted")
	})
	connection := provider.FromPreset("openai-chat", "key", server.BaseURL(providersim.Chat))
	connection.Model = "adaptive-chunk-cancel"
	connection.SetContextWindow(64_000)
	agent := newTestAgent(t, connection, connection.Model)
	history := []provider.ChatMessage{provider.NewTextMessage("assistant", strings.Repeat("old state ", 100_000)), provider.NewTextMessage("user", "preserve request")}
	original, err := json.Marshal(history)
	if err != nil {
		t.Fatal(err)
	}
	result, err := agent.Compact(ctx, history, "system", nil)
	if !errors.Is(err, context.Canceled) || len(result.Messages) != 0 {
		t.Fatalf("cancelled extraction installed: result=%+v err=%v calls=%d", result, err, calls.Load())
	}
	mu.Lock()
	for _, sent := range seen {
		if sent != 1 {
			t.Errorf("a part was sent %d times after the compaction was cancelled", sent)
		}
	}
	mu.Unlock()
	unchanged, err := json.Marshal(history)
	if err != nil || string(unchanged) != string(original) {
		t.Fatal("cancelled chunk extraction altered source")
	}
}

func TestCompactSizesHugeHumanInputForContinuationModel(t *testing.T) {
	server := providersim.New(t)
	server.Respond(func(r providersim.Request) providersim.Turn {
		return providersim.Text("- assignment: preserve ownership, parser.go and publish_report obligation")
	})
	connection := provider.FromPreset("openai-chat", "key", server.BaseURL(providersim.Chat))
	connection.Model = "adaptive-large-extractor-small-target"
	connection.SetContextWindow(128_000)
	agent := newTestAgent(t, connection, connection.Model)
	agent.CompactionTargetWindow = 32_000
	agent.CompactionTargetTokens = 20_000
	agent.Depth = 1
	assignment := provider.NewTextMessage("user", "parent assignment\n"+strings.Repeat("inspect parser and preserve evidence ", 10_000)+"\ncall publish_report")
	assignment.Origin = provider.OriginAssignment
	result, err := agent.Compact(context.Background(), []provider.ChatMessage{assignment, provider.NewTextMessage("assistant", "work underway")}, "child system", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.AfterTokens >= 20_000 || len(result.Messages) != 2 || !strings.Contains(result.Messages[0].ContentString(), "publish_report") || result.Messages[1].EffectiveOrigin() != provider.OriginRuntimeContext {
		t.Fatalf("oversized assignment defeated continuation budget: %+v", result)
	}
	if assignment.ContentString() == "" || !strings.Contains(assignment.ContentString(), "parent assignment") {
		t.Fatal("original assignment was mutated")
	}
}

func TestRunRequestAdmissionUsesMeasuredBaseline(t *testing.T) {
	var calls atomic.Int32
	server := providersim.New(t)
	server.Respond(func(r providersim.Request) providersim.Turn {
		calls.Add(1)
		return providersim.Text("accepted measured context")
	})
	connection := provider.FromPreset("openai-chat", "key", server.BaseURL(providersim.Chat))
	connection.Model = "adaptive-measured-admission"
	connection.SetContextWindow(64_000)
	agent := newTestAgent(t, connection, connection.Model)
	agent.SeedContextUsage(20_000, 100, 2)
	history := []provider.ChatMessage{
		provider.NewTextMessage("user", strings.Repeat("expensive local estimate ", 20_000)),
		provider.NewTextMessage("assistant", "previous answer"),
		provider.NewTextMessage("user", "continue"),
	}
	var events []AgentEvent
	agent.Run(context.Background(), history, "system", func(event AgentEvent) { events = append(events, event) }, nil, "", nil)
	if calls.Load() != 1 || hasEvent(events, "error") || hasEvent(events, "context_compacted") {
		t.Fatalf("local guard contradicted measured baseline: calls=%d events=%+v", calls.Load(), events)
	}
}

func TestExtractorLearnsFromReportedUsage(t *testing.T) {
	var calls atomic.Int32
	server := providersim.New(t)
	server.Respond(func(r providersim.Request) providersim.Turn {
		body := canonicalChatRequest(t, r)
		calls.Add(1)
		input := estimateCanonicalRequest(body.Messages, nil, body.Model)
		return providersim.Text("- facts: preserved").WithUsage(providersim.Usage{Input: input * 2, Output: 10})
	})
	connection := provider.FromPreset("openai-chat", "key", server.BaseURL(providersim.Chat))
	connection.Model = "adaptive-usage-calibration"
	connection.SetContextWindow(64_000)
	agent := newTestAgent(t, connection, connection.Model)
	extractor := compactionExtractor{
		agent: agent, ctx: context.Background(), emit: func(AgentEvent) {}, requestID: "calibration", role: "root",
		stage: compactionStagesForTest[0], system: provider.NewTextMessage("system", "system"),
		common: provider.NewTextMessage("user", "extract factual source"), limits: agent.extractionLimits(), outputBudget: 2000,
	}
	source := []provider.ChatMessage{provider.NewTextMessage("assistant", strings.Repeat("evidence ", 5000))}
	if _, err := extractor.request(source, "full state"); err != nil {
		t.Fatal(err)
	}
	if extractor.limits.admissible() > 30_400 || calls.Load() != 1 {
		t.Fatalf("provider usage not calibrated: budget=%d calls=%d", extractor.limits.admissible(), calls.Load())
	}
}

func TestRunWithoutUsageCountsToolSchemasInContextBaseline(t *testing.T) {
	server := providersim.New(t)
	server.Script(providersim.Text("complete").WithoutUsage())
	connection := provider.FromPreset("openai-chat", "key", server.BaseURL(providersim.Chat))
	connection.Model = "adaptive-no-usage-schema"
	agent := newTestAgent(t, connection, connection.Model)
	var contextTokens int
	agent.Run(context.Background(), []provider.ChatMessage{provider.NewTextMessage("user", "inspect")}, "system", func(event AgentEvent) {
		if event.Type == "usage" {
			data := event.Data.(map[string]any)
			contextTokens, _ = data["context_prompt"].(int)
		}
	}, nil, "", nil)
	r, ok := server.Last()
	if !ok {
		t.Fatal("no request reached the provider")
	}
	requestBody := canonicalChatRequest(t, r)
	if len(requestBody.Tools) == 0 || contextTokens <= provider.EstimateMessagesTotal(requestBody.Messages, connection.Model) {
		t.Fatalf("tool schemas omitted from fallback context: tokens=%d tools=%d", contextTokens, len(requestBody.Tools))
	}
}

func BenchmarkExtractionCursorOversizedToolResult(benchmark *testing.B) {
	message := provider.NewTextMessage("tool", strings.Repeat("test output with evidence\n", 80_000))
	message.ToolCallID = "large-tool-result"
	benchmark.ReportAllocs()
	for benchmark.Loop() {
		for cursor := newExtractionCursor([]provider.ChatMessage{message}); !cursor.done(); {
			_, cursor = cursor.take(60_000, 0)
		}
	}
}
