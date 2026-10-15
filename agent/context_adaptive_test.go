package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"crude/provider"
)

func TestCompactOversizedHistoryWithSmallerExtractionModel(t *testing.T) {
	var calls atomic.Int32
	var requests []provider.ChatRequest
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body provider.ChatRequest
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		requests = append(requests, body)
		input := estimateCanonicalRequest(body.Messages, body.Tools, body.Model)
		output := max(body.MaxTokens, body.MaxCompletionTokens)
		if input+output >= 256_000 {
			t.Errorf("oversized extraction sent: input=%d output=%d", input, output)
		}
		writeChatSSE(writer, fmt.Sprintf("- extraction %d: evidence retained", calls.Add(1)), "stop")
	}))
	defer server.Close()
	connection := provider.FromPreset("openai-chat", "key", server.URL)
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
	chunks, err := splitExtractionSource(source, 2000, "model")
	if err != nil {
		t.Fatal(err)
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
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body provider.ChatRequest
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		attempts.Add(1)
		if estimateCanonicalRequest(body.Messages, nil, body.Model) > 30_000 {
			rejected.Add(1)
			writer.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprint(writer, `{"error":{"code":"context_length_exceeded","message":"maximum context length exceeded"}}`)
			return
		}
		writeChatSSE(writer, "- parser.go: evidence preserved", "stop")
	}))
	defer server.Close()
	connection := provider.FromPreset("openai-chat", "key", server.URL)
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
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body provider.ChatRequest
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		requests = append(requests, body)
		if calls.Add(1) == 1 {
			writer.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprint(writer, `{"error":{"type":"invalid_request_error","message":"The input is longer than the model's context length"}}`)
			return
		}
		if body.ToolChoice == "none" {
			writeChatSSE(writer, "- parser constraint: retained", "stop")
			return
		}
		writeChatSSE(writer, "recovered", "stop")
	}))
	defer server.Close()
	connection := provider.FromPreset("openai-chat", "key", server.URL)
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
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body provider.ChatRequest
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		calls.Add(1)
		if body.ToolChoice == "none" {
			writeChatSSE(writer, "- child report: preserved", "stop")
		} else {
			writeChatSSE(writer, "continued", "stop")
		}
	}))
	defer server.Close()
	connection := provider.FromPreset("openai-chat", "key", server.URL)
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
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				if failure == "unauthorized" {
					writer.WriteHeader(http.StatusUnauthorized)
					return
				}
				if failure == "quota" {
					writer.WriteHeader(http.StatusTooManyRequests)
					_, _ = fmt.Fprint(writer, `{"error":{"code":"insufficient_quota","message":"billing limit"}}`)
					return
				}
				writeChatSSE(writer, "apparently valid partial extraction", "length")
			}))
			defer server.Close()
			connection := provider.FromPreset("openai-chat", "key", server.URL)
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
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 2 {
			cancel()
			return
		}
		writeChatSSE(writer, "- first chunk: extracted", "stop")
	}))
	defer server.Close()
	connection := provider.FromPreset("openai-chat", "key", server.URL)
	connection.Model = "adaptive-chunk-cancel"
	connection.SetContextWindow(64_000)
	agent := newTestAgent(t, connection, connection.Model)
	history := []provider.ChatMessage{provider.NewTextMessage("assistant", strings.Repeat("old state ", 100_000)), provider.NewTextMessage("user", "preserve request")}
	original, err := json.Marshal(history)
	if err != nil {
		t.Fatal(err)
	}
	result, err := agent.Compact(ctx, history, "system", nil)
	if !errors.Is(err, context.Canceled) || len(result.Messages) != 0 || calls.Load() != 2 {
		t.Fatalf("cancelled extraction retried or installed: result=%+v err=%v calls=%d", result, err, calls.Load())
	}
	unchanged, err := json.Marshal(history)
	if err != nil || string(unchanged) != string(original) {
		t.Fatal("cancelled chunk extraction altered source")
	}
}

func TestCompactSizesHugeHumanInputForContinuationModel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writeChatSSE(writer, "- assignment: preserve ownership, parser.go and publish_report obligation", "stop")
	}))
	defer server.Close()
	connection := provider.FromPreset("openai-chat", "key", server.URL)
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
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writeChatSSE(writer, "accepted measured context", "stop")
	}))
	defer server.Close()
	connection := provider.FromPreset("openai-chat", "key", server.URL)
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
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body provider.ChatRequest
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		calls.Add(1)
		input := estimateCanonicalRequest(body.Messages, nil, body.Model)
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(writer, "data: {\"choices\":[{\"delta\":{\"content\":\"- facts: preserved\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":%d,\"completion_tokens\":10}}\n\ndata: [DONE]\n\n", input*2)
	}))
	defer server.Close()
	connection := provider.FromPreset("openai-chat", "key", server.URL)
	connection.Model = "adaptive-usage-calibration"
	connection.SetContextWindow(64_000)
	agent := newTestAgent(t, connection, connection.Model)
	extractor := compactionExtractor{
		agent: agent, ctx: context.Background(), emit: func(AgentEvent) {}, requestID: "calibration", role: "root",
		stage: compactionStagesForTest[0], system: provider.NewTextMessage("system", "system"),
		common: provider.NewTextMessage("user", "extract factual source"), inputBudget: 55_000, capacityBudget: 55_000, outputBudget: 2000,
	}
	source := []provider.ChatMessage{provider.NewTextMessage("assistant", strings.Repeat("evidence ", 5000))}
	if _, err := extractor.request(source, "full state", 0); err != nil {
		t.Fatal(err)
	}
	if extractor.inputBudget > 27_500 || calls.Load() != 1 {
		t.Fatalf("provider usage not calibrated: budget=%d calls=%d", extractor.inputBudget, calls.Load())
	}
}

func TestRunWithoutUsageCountsToolSchemasInContextBaseline(t *testing.T) {
	var requestBody provider.ChatRequest
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if err := json.NewDecoder(request.Body).Decode(&requestBody); err != nil {
			t.Error(err)
		}
		writeChatSSE(writer, "complete", "stop")
	}))
	defer server.Close()
	connection := provider.FromPreset("openai-chat", "key", server.URL)
	connection.Model = "adaptive-no-usage-schema"
	agent := newTestAgent(t, connection, connection.Model)
	var contextTokens int
	agent.Run(context.Background(), []provider.ChatMessage{provider.NewTextMessage("user", "inspect")}, "system", func(event AgentEvent) {
		if event.Type == "usage" {
			data := event.Data.(map[string]any)
			contextTokens, _ = data["context_prompt"].(int)
		}
	}, nil, "", nil)
	if len(requestBody.Tools) == 0 || contextTokens <= provider.EstimateMessagesTotal(requestBody.Messages, connection.Model) {
		t.Fatalf("tool schemas omitted from fallback context: tokens=%d tools=%d", contextTokens, len(requestBody.Tools))
	}
}

func BenchmarkSplitExtractionOversizedToolResult(benchmark *testing.B) {
	message := provider.NewTextMessage("tool", strings.Repeat("test output with evidence\n", 80_000))
	message.ToolCallID = "large-tool-result"
	benchmark.ReportAllocs()
	for benchmark.Loop() {
		if _, err := splitExtractionSource([]provider.ChatMessage{message}, 60_000, "model"); err != nil {
			benchmark.Fatal(err)
		}
	}
}
