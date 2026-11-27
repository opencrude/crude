package turnloop

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"crude/agent/provider"
	"crude/internal/providersim"
)

func TestCompactionWorkStageRecoversReasoningOutputExhaustion(t *testing.T) {
	var requests []map[string]json.RawMessage
	var limits []int
	var workInputs []int
	var truncated atomic.Int32
	server := providersim.New(t, providersim.WithChatGateway("cmd/deepseek/deepseek-v4-flash", providersim.DeepSeek))
	var observations sync.Mutex
	server.Respond(func(r providersim.Request) providersim.Turn {
		observations.Lock()
		defer observations.Unlock()
		var body map[string]json.RawMessage
		if err := json.Unmarshal(r.Raw, &body); err != nil {
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
		work := strings.Contains(messages[len(messages)-1].ContentString(), compactionStagesForTest[1].Root)
		if work {
			limits = append(limits, cap)
			workInputs = append(workInputs, provider.EstimateMessagesTotal(messages, "cmd/deepseek/deepseek-v4-flash"))
		}
		if effort != "high" || body["thinking"] != nil {
			t.Errorf("compaction lost configured reasoning: effort=%q thinking=%s", effort, body["thinking"])
		}
		if work && workInputs[len(workInputs)-1] > 30_000 && cap < 48_000 {
			truncated.Add(1)
			return providersim.Text("UNSAFE_TRUNCATED_CHECKPOINT").WithReasoning("reasoning consumed the cap", "", "").WithFinish(providersim.FinishLength).WithUsage(providersim.Usage{Input: 598300, Output: cap, Reasoning: max(0, cap-100)})
		}
		return providersim.Text("- parser.go: no-delete constraint; work and evidence retained")
	})
	connection := provider.FromPreset("commandcode", "key", server.BaseURL(providersim.Chat))
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
	if len(requests) <= len(compactionStagesForTest) || connection.ThinkingEffort() != "high" || strings.Contains(result.Messages[0].ContentString(), "UNSAFE_TRUNCATED_CHECKPOINT") {
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
	server := providersim.New(t)
	var observations sync.Mutex
	server.Respond(func(r providersim.Request) providersim.Turn {
		observations.Lock()
		defer observations.Unlock()
		var body struct {
			MaxTokens int `json:"max_tokens"`
		}
		if err := json.Unmarshal(r.Raw, &body); err != nil {
			t.Error(err)
		}
		caps = append(caps, body.MaxTokens)
		return providersim.Text("- exact evidence preserved")
	})
	connection := provider.FromPreset("openai-chat", "key", server.BaseURL(providersim.Chat))
	connection.Model = "configured-output-extractor"
	connection.SetContextWindow(100_000)
	agent := newTestAgent(t, connection, connection.Model)
	agent.MaxOutputTokens = 2048
	_, err := agent.Compact(context.Background(), compactionTestHistory(provider.NewTextMessage("user", "preserve this request")), "system", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(caps) != len(compactionStagesForTest) {
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
	server := providersim.New(t)
	var observations sync.Mutex
	server.Respond(func(r providersim.Request) providersim.Turn {
		observations.Lock()
		defer observations.Unlock()
		var body map[string]json.RawMessage
		if err := json.Unmarshal(r.Raw, &body); err != nil {
			t.Error(err)
		}
		bodies = append(bodies, body)
		return providersim.Text("- work state preserved")
	})
	connection := provider.FromPreset("commandcode", "key", server.BaseURL(providersim.Chat))
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
	if len(bodies) != len(compactionStagesForTest)+1 {
		t.Fatalf("calls=%d", len(bodies))
	}
	for _, body := range bodies[:len(compactionStagesForTest)] {
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
	server := providersim.New(t)
	var observations sync.Mutex
	server.Respond(func(r providersim.Request) providersim.Turn {
		observations.Lock()
		defer observations.Unlock()
		body := canonicalChatRequest(t, r)
		requests = append(requests, provider.EstimateMessagesTotal(body.Messages, body.Model))
		if requests[len(requests)-1] > 10_000 {
			return providersim.Text("UNSAFE_TRUNCATED_CHECKPOINT").WithFinish("model_context_window_exceeded")
		}
		return providersim.Text("- verified state and constraints preserved")
	})
	connection := provider.FromPreset("openai-chat", "key", server.BaseURL(providersim.Chat))
	connection.Model = "context-truncation-extractor"
	connection.SetContextWindow(100_000)
	agent := newTestAgent(t, connection, connection.Model)
	result, err := agent.Compact(context.Background(), compactionTestHistory(provider.NewTextMessage("user", "retain this request")), "system", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(requests) <= len(compactionStagesForTest) || requests[1] >= requests[0] || strings.Contains(result.Messages[0].ContentString(), "UNSAFE_TRUNCATED_CHECKPOINT") {
		t.Fatalf("truncated input was retried without reduction: request estimates=%v", requests)
	}
}

func TestCompactionWithVeryLargeRealRequestKeepsContextWithinWindow(t *testing.T) {
	var capacities []struct{ input, output int }
	server := providersim.New(t)
	var observations sync.Mutex
	server.Respond(func(r providersim.Request) providersim.Turn {
		observations.Lock()
		defer observations.Unlock()
		body := canonicalChatRequest(t, r)
		capacities = append(capacities, struct{ input, output int }{
			input:  provider.EstimateMessagesTotal(body.Messages, body.Model),
			output: max(body.MaxTokens, body.MaxCompletionTokens),
		})
		return providersim.Text("- work and constraints preserved")
	})
	connection := provider.FromPreset("commandcode", "key", server.BaseURL(providersim.Chat))
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
	if len(capacities) != len(compactionStagesForTest) || result.AfterTokens >= result.BeforeTokens {
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
	server := providersim.New(t)
	server.Respond(func(r providersim.Request) providersim.Turn {
		calls++
		if calls == 1 {
			return providersim.Text(strings.Repeat("evidence requires refinement ", 800))
		}
		return providersim.Text("- retained: all constraints and evidence")
	})
	connection := provider.FromPreset("openai-chat", "key", server.BaseURL(providersim.Chat))
	connection.Model = "refine-successful-extraction"
	connection.SetContextWindow(32_000)
	agent := newTestAgent(t, connection, connection.Model)
	extractor := compactionExtractor{
		agent: agent, ctx: context.Background(), emit: func(AgentEvent) {},
		stage: compactionStagesForTest[0], role: "root", system: provider.NewTextMessage("system", "system"),
		common: provider.NewTextMessage("user", "preserve exact evidence"),
		limits: agent.extractionLimits(), outputBudget: 200,
	}
	text, err := extractor.request([]provider.ChatMessage{provider.NewTextMessage("user", "preserve task")}, "extract")
	if err != nil || calls != 2 || !strings.Contains(text, "all constraints and evidence") {
		t.Fatalf("refinement result=%q calls=%d error=%v", text, calls, err)
	}
}

func TestCompactionUsesFullRemainingGenerationCapacityWithReasoning(t *testing.T) {
	var capturedInput, capturedOutput int
	server := providersim.New(t)
	var observations sync.Mutex
	server.Respond(func(r providersim.Request) providersim.Turn {
		observations.Lock()
		defer observations.Unlock()
		body := canonicalChatRequest(t, r)
		if capturedOutput == 0 {
			// The serving model declares the conversation's tools; Crude budgets
			// them with its own estimate of those definitions.
			capturedInput = estimateCanonicalRequest(body.Messages, allToolDefs(), body.Model)
			capturedOutput = max(body.MaxTokens, body.MaxCompletionTokens)
		}
		return providersim.Text("- preserved facts")
	})
	connection := provider.FromPreset("commandcode", "key", server.BaseURL(providersim.Chat))
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
	server := providersim.New(t)
	var observations sync.Mutex
	server.Respond(func(r providersim.Request) providersim.Turn {
		observations.Lock()
		defer observations.Unlock()
		body := canonicalChatRequest(t, r)
		estimated := estimateCanonicalRequest(body.Messages, allToolDefs(), body.Model)
		measured := estimated * 2
		measuredInputs = append(measuredInputs, estimated)
		outputs = append(outputs, max(body.MaxTokens, body.MaxCompletionTokens))
		return providersim.Text("- evidence preserved").WithUsage(providersim.Usage{Input: measured, Output: 5})
	})
	connection := provider.FromPreset("openai-chat", "key", server.BaseURL(providersim.Chat))
	connection.Model = "measured-extraction-capacity"
	connection.SetContextWindow(128_000)
	agent := newTestAgent(t, connection, connection.Model)
	agent.MaxOutputTokens = 128_000
	if _, err := agent.Compact(context.Background(), compactionTestHistory(provider.NewTextMessage("user", "preserve request")), "system", nil); err != nil {
		t.Fatal(err)
	}
	if len(outputs) != len(compactionStagesForTest) || outputs[1] >= outputs[0] {
		t.Fatalf("provider-measured input did not reduce next response allowance: estimates=%v allowances=%v", measuredInputs, outputs)
	}
	want := 128_000 - 2*measuredInputs[1] - contextSafetyReserve(128_000)
	if outputs[1] != want {
		t.Fatalf("calibrated response allowance=%d want=%d", outputs[1], want)
	}
}

func TestCompactionLearnsProviderOutputCeilingAcrossSessions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "model-catalog.db")
	catalog, err := provider.OpenModelCatalog(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	provider.UseModelCatalog(catalog)
	t.Cleanup(func() { _ = catalog.Close() })

	var outputs []int
	var retainedSource bool
	var firstTools string
	server := providersim.New(t)
	var observations sync.Mutex
	server.Respond(func(r providersim.Request) providersim.Turn {
		observations.Lock()
		defer observations.Unlock()
		limit, err := r.OutputLimit.Int64()
		if err != nil {
			t.Error(err)
		}
		output := int(limit)
		outputs = append(outputs, output)
		if len(r.Messages) > 0 && strings.Contains(simulatedText(r.Messages[0].Parts), "original evidence") {
			retainedSource = true
		}
		// Only the output cap may change: the serving model keeps declaring the
		// same tools, and tool use stays forbidden.
		names := make([]string, 0, len(r.Tools))
		for _, tool := range r.Tools {
			names = append(names, tool.Name)
		}
		declared := strings.Join(names, ",")
		if firstTools == "" {
			firstTools = declared
		}
		if string(r.ToolChoice) != `"none"` || declared == "" || declared != firstTools || r.Model != "cmd/deepseek/deepseek-v4-flash" ||
			simulatedText(r.Messages[len(r.Messages)-1].Parts) == "" {
			t.Errorf("extraction request was changed instead of tuning its output cap: tool_choice=%v tools=%d model=%s", r.ToolChoice, len(r.Tools), r.Model)
		}
		if output > 393216 {
			return providersim.HTTPError(providersim.InvalidRequest).WithFaultMessage(fmt.Sprintf(`{"code":400, "reason":"INVALID_REQUEST_BODY", "message":"max_tokens (current value: %d) must be between 0 and 393216 ", "metadata":{}}`, output))
		}
		return providersim.Text("- original evidence and human corrections retained")
	})
	makeAgent := func() *Agent {
		connection := provider.FromPreset("commandcode", "key", server.BaseURL(providersim.Chat))
		connection.Model = "cmd/deepseek/deepseek-v4-flash"
		connection.AccountID = "test-account"
		connection.SetContextWindow(1_050_000)
		connection.SetThinkingEffort("high")
		a := newTestAgent(t, connection, connection.Model)
		a.MaxOutputTokens = 600_000
		a.CompactionAgentProfile = "default/errand"
		// These replies answer one section per request.
		if err := provider.RecordRequestLimit(a.CompactionAgentProfile, connection, connection.Model, provider.LearnedSectioned, 1, provider.InferredLimitLifetime); err != nil {
			t.Fatal(err)
		}
		return a
	}
	first := makeAgent()
	history := []provider.ChatMessage{provider.NewTextMessage("assistant", strings.Repeat("original evidence ", 30000)), provider.NewTextMessage("user", "retain my correction")}
	result, err := first.Compact(context.Background(), history, "stable system", nil)
	if err != nil || len(result.Messages) == 0 || !retainedSource {
		t.Fatalf("rejected extraction did not self-tune and preserve source: result=%+v err=%v", result, err)
	}
	if len(outputs) != 5 || outputs[0] <= 393216 {
		t.Fatalf("expected one over-limit request and four bounded stage requests; output caps=%v", outputs)
	}
	// A stated range can be exclusive, so the ceiling is one below it.
	for _, cap := range outputs[1:] {
		if cap != 393215 {
			t.Fatalf("output ceiling not learned after first rejection: %v", outputs)
		}
	}
	if err := catalog.Close(); err != nil {
		t.Fatal(err)
	}
	catalog, err = provider.OpenModelCatalog(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	provider.UseModelCatalog(catalog)
	outputs = nil
	second := makeAgent()
	if _, err := second.Compact(context.Background(), history, "stable system", nil); err != nil {
		t.Fatalf("learned ceiling did not survive catalog reopen: %v", err)
	}
	if len(outputs) != 4 {
		t.Fatalf("new agent session retried the known-invalid output cap: %v", outputs)
	}
	for _, cap := range outputs {
		if cap != 393215 {
			t.Fatalf("reopened catalog did not bound extraction: %v", outputs)
		}
	}
}
