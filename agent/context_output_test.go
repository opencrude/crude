package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"crude/provider"
)

func TestCompactionWorkStageRecoversReasoningOutputExhaustion(t *testing.T) {
	var requests []map[string]json.RawMessage
	var limits []int
	var workInputs []int
	var truncated atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]json.RawMessage
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		requests = append(requests, body)
		var messages []provider.ChatMessage
		if err := json.Unmarshal(body["messages"], &messages); err != nil {
			t.Error(err)
		}
		var cap int
		_ = json.Unmarshal(body["max_tokens"], &cap)
		var effort string
		_ = json.Unmarshal(body["reasoning_effort"], &effort)
		work := strings.Contains(messages[len(messages)-1].ContentString(), compactionStages[1].Root)
		if work {
			limits = append(limits, cap)
			workInputs = append(workInputs, provider.EstimateMessagesTotal(messages, "cmd/deepseek/deepseek-v4-flash"))
		}
		if effort != "high" || body["thinking"] != nil {
			t.Errorf("compaction lost configured reasoning: effort=%q thinking=%s", effort, body["thinking"])
		}
		if work && workInputs[len(workInputs)-1] > 30_000 && cap < 48_000 {
			truncated.Add(1)
			writer.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprintf(writer, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"reasoning consumed the cap\",\"content\":\"UNSAFE_TRUNCATED_CHECKPOINT\"},\"finish_reason\":\"length\"}],\"usage\":{\"prompt_tokens\":598300,\"completion_tokens\":%d,\"completion_tokens_details\":{\"reasoning_tokens\":%d}}}\n\ndata: [DONE]\n\n", cap, max(0, cap-100))
			return
		}
		writeChatSSE(writer, "- parser.go: no-delete constraint; work and evidence retained", "stop")
	}))
	defer server.Close()
	connection := provider.FromPreset("commandcode", "key", server.URL)
	connection.Model = "cmd/deepseek/deepseek-v4-flash"
	connection.SetContextWindow(128_000)
	connection.SetThinkingEffort("high")
	agent := newTestAgent(t, connection, connection.Model)
	agent.MaxOutputTokens = 44_000
	agent.compactionRetryDelay = func(int) time.Duration { return 0 }
	history := compactionTestHistory(provider.NewTextMessage("user", "Keep parser.go; preserve failing test evidence."))
	result, err := agent.Compact(context.Background(), history, "stable system", nil)
	if err != nil {
		t.Fatal(err)
	}
	if truncated.Load() != 1 || len(limits) < 2 || limits[0] <= 3936 || workInputs[1] >= workInputs[0] {
		t.Fatalf("work extraction repeated the exhausted source: truncations=%d caps=%v inputs=%v", truncated.Load(), limits, workInputs)
	}
	if len(requests) <= len(compactionStages) || connection.ThinkingEffort() != "high" || strings.Contains(result.Messages[0].ContentString(), "UNSAFE_TRUNCATED_CHECKPOINT") {
		t.Fatalf("checkpoint recovery changed normal reasoning or installed a truncated extraction: calls=%d effort=%q", len(requests), connection.ThinkingEffort())
	}
	for _, request := range requests {
		if string(request["reasoning_effort"]) != `"high"` || request["thinking"] != nil {
			t.Fatalf("recovery request changed model thinking: effort=%s thinking=%s", request["reasoning_effort"], request["thinking"])
		}
	}
}

func TestCompactionPreservesConfiguredModelOutputCap(t *testing.T) {
	var caps []int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body struct {
			MaxTokens int `json:"max_tokens"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		caps = append(caps, body.MaxTokens)
		writeChatSSE(writer, "- exact evidence preserved", "stop")
	}))
	defer server.Close()
	connection := provider.FromPreset("openai-chat", "key", server.URL)
	connection.Model = "configured-output-extractor"
	connection.SetContextWindow(100_000)
	agent := newTestAgent(t, connection, connection.Model)
	agent.MaxOutputTokens = 2048
	_, err := agent.Compact(context.Background(), compactionTestHistory(provider.NewTextMessage("user", "preserve this request")), "system", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(caps) != len(compactionStages) {
		t.Fatalf("requests=%d", len(caps))
	}
	for _, cap := range caps {
		if cap > 2048 || cap <= 0 {
			t.Fatalf("configured output cap ignored: %d", cap)
		}
	}
}

func TestCompactionKeepsConfiguredReasoningForExtractionAndOrdinaryRequests(t *testing.T) {
	var bodies []map[string]json.RawMessage
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]json.RawMessage
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		bodies = append(bodies, body)
		writeChatSSE(writer, "- work state preserved", "stop")
	}))
	defer server.Close()
	connection := provider.FromPreset("commandcode", "key", server.URL)
	connection.Model = "cmd/deepseek/deepseek-v4-flash"
	connection.SetContextWindow(128_000)
	connection.SetThinkingEffort("high")
	agent := newTestAgent(t, connection, connection.Model)
	if _, err := agent.Compact(context.Background(), compactionTestHistory(provider.NewTextMessage("user", "preserve task")), "system", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := agent.completeStream(context.Background(), []provider.ChatMessage{provider.NewTextMessage("user", "normal request")}, nil, nil, nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != len(compactionStages)+1 {
		t.Fatalf("calls=%d", len(bodies))
	}
	for _, body := range bodies[:len(compactionStages)] {
		if string(body["reasoning_effort"]) != `"high"` || body["thinking"] != nil {
			t.Fatalf("compaction altered the configured reasoning: effort=%s thinking=%s", body["reasoning_effort"], body["thinking"])
		}
	}
	if string(bodies[len(bodies)-1]["reasoning_effort"]) != `"high"` || bodies[len(bodies)-1]["thinking"] != nil {
		t.Fatalf("ordinary provider request was changed by compaction: effort=%s thinking=%s", bodies[len(bodies)-1]["reasoning_effort"], bodies[len(bodies)-1]["thinking"])
	}
}

func TestCompactionContextTruncationReplansInsteadOfRepeatingTheSameRequest(t *testing.T) {
	var requests []int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body provider.ChatRequest
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		requests = append(requests, provider.EstimateMessagesTotal(body.Messages, body.Model))
		if requests[len(requests)-1] > 10_000 {
			writeChatSSE(writer, "UNSAFE_TRUNCATED_CHECKPOINT", "model_context_window_exceeded")
			return
		}
		writeChatSSE(writer, "- verified state and constraints preserved", "stop")
	}))
	defer server.Close()
	connection := provider.FromPreset("openai-chat", "key", server.URL)
	connection.Model = "context-truncation-extractor"
	connection.SetContextWindow(100_000)
	agent := newTestAgent(t, connection, connection.Model)
	result, err := agent.Compact(context.Background(), compactionTestHistory(provider.NewTextMessage("user", "retain this request")), "system", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(requests) <= len(compactionStages) || requests[1] >= requests[0] || strings.Contains(result.Messages[0].ContentString(), "UNSAFE_TRUNCATED_CHECKPOINT") {
		t.Fatalf("truncated input was retried without reduction: request estimates=%v", requests)
	}
}

func TestCompactionWithVeryLargeRealRequestKeepsContextWithinWindow(t *testing.T) {
	var capacities []struct{ input, output int }
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body provider.ChatRequest
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		capacities = append(capacities, struct{ input, output int }{
			input:  provider.EstimateMessagesTotal(body.Messages, body.Model),
			output: max(body.MaxTokens, body.MaxCompletionTokens),
		})
		writeChatSSE(writer, "- work and constraints preserved", "stop")
	}))
	defer server.Close()
	connection := provider.FromPreset("commandcode", "key", server.URL)
	connection.Model = "cmd/deepseek/deepseek-v4-flash"
	connection.SetContextWindow(1_050_000)
	connection.SetThinkingEffort("high")
	agent := newTestAgent(t, connection, connection.Model)
	history := []provider.ChatMessage{
		provider.NewTextMessage("assistant", strings.Repeat("source evidence ", 150_000)),
		provider.NewTextMessage("user", "preserve correction and current task"),
	}
	result, err := agent.Compact(context.Background(), history, "system", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(capacities) != len(compactionStages) || result.AfterTokens >= result.BeforeTokens {
		t.Fatalf("compaction could not use the available large window: requests=%d result=%+v", len(capacities), result)
	}
	for _, capacity := range capacities {
		if capacity.input+capacity.output+contextSafetyReserve(1_050_000) > 1_050_000 || capacity.output <= 3936 {
			t.Fatalf("extraction did not leave output headroom: %+v", capacity)
		}
	}
}

func TestCompactionRefinesOnlyOversizedSuccessfulCheckpointText(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			writeChatSSE(writer, strings.Repeat("evidence requires refinement ", 800), "stop")
			return
		}
		writeChatSSE(writer, "- retained: all constraints and evidence", "stop")
	}))
	defer server.Close()
	connection := provider.FromPreset("openai-chat", "key", server.URL)
	connection.Model = "refine-successful-extraction"
	connection.SetContextWindow(32_000)
	agent := newTestAgent(t, connection, connection.Model)
	extractor := compactionExtractor{
		agent: agent, ctx: context.Background(), emit: func(AgentEvent) {},
		stage: compactionStages[0], role: "root", system: provider.NewTextMessage("system", "system"),
		common:      provider.NewTextMessage("user", "preserve exact evidence"),
		inputBudget: 30_000, capacityBudget: 30_000, outputBudget: 200, responseBudget: 2000,
	}
	text, err := extractor.request([]provider.ChatMessage{provider.NewTextMessage("user", "preserve task")}, "extract", 0)
	if err != nil || calls != 2 || !strings.Contains(text, "all constraints and evidence") {
		t.Fatalf("refinement result=%q calls=%d error=%v", text, calls, err)
	}
}

func TestCompactionUsesFullRemainingGenerationCapacityWithReasoning(t *testing.T) {
	var capturedInput, capturedOutput int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body provider.ChatRequest
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if capturedOutput == 0 {
			capturedInput = provider.EstimateMessagesTotal(body.Messages, body.Model)
			capturedOutput = max(body.MaxTokens, body.MaxCompletionTokens)
		}
		writeChatSSE(writer, "- preserved facts", "stop")
	}))
	defer server.Close()
	connection := provider.FromPreset("commandcode", "key", server.URL)
	connection.Model = "cmd/deepseek/deepseek-v4-flash"
	connection.SetContextWindow(128_000)
	connection.SetThinkingEffort("high")
	agent := newTestAgent(t, connection, connection.Model)
	agent.MaxOutputTokens = 128_000
	if _, err := agent.Compact(context.Background(), compactionTestHistory(provider.NewTextMessage("user", "keep work and constraints")), "system", nil); err != nil {
		t.Fatal(err)
	}
	want := min(agent.MaxOutputTokens, connection.ContextWindow(connection.Model)-capturedInput-contextSafetyReserve(connection.ContextWindow(connection.Model)))
	if capturedOutput != want || capturedOutput <= connection.ContextWindow(connection.Model)/4 {
		t.Fatalf("generation allowance=%d want full available %d input=%d", capturedOutput, want, capturedInput)
	}
}

func TestCompactionUsesReportedInputToLeaveRoomForReasoning(t *testing.T) {
	var measuredInputs []int
	var outputs []int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body provider.ChatRequest
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		estimated := provider.EstimateMessagesTotal(body.Messages, body.Model)
		measured := estimated * 2
		measuredInputs = append(measuredInputs, estimated)
		outputs = append(outputs, max(body.MaxTokens, body.MaxCompletionTokens))
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(writer, "data: {\"choices\":[{\"delta\":{\"content\":\"- evidence preserved\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":%d,\"completion_tokens\":5}}\n\ndata: [DONE]\n\n", measured)
	}))
	defer server.Close()
	connection := provider.FromPreset("openai-chat", "key", server.URL)
	connection.Model = "measured-extraction-capacity"
	connection.SetContextWindow(128_000)
	agent := newTestAgent(t, connection, connection.Model)
	agent.MaxOutputTokens = 128_000
	if _, err := agent.Compact(context.Background(), compactionTestHistory(provider.NewTextMessage("user", "preserve request")), "system", nil); err != nil {
		t.Fatal(err)
	}
	if len(outputs) != len(compactionStages) || outputs[1] >= outputs[0] {
		t.Fatalf("provider-measured input did not reduce next response allowance: estimates=%v allowances=%v", measuredInputs, outputs)
	}
	want := 128_000 - 2*measuredInputs[1] - contextSafetyReserve(128_000)
	if outputs[1] != want {
		t.Fatalf("calibrated response allowance=%d want=%d", outputs[1], want)
	}
}
