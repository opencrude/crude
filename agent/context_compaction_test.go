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

	rootprompt "crude/prompt"
	"crude/provider"
)

var compactionStagesForTest = rootprompt.CompactionStages()

func TestCompactionThresholdReservesSummaryHeadroom(t *testing.T) {
	if got := compactionThreshold(1_050_000, 40); got != 420_000 {
		t.Fatalf("threshold = %d, want 420000", got)
	}
	if got := compactionThreshold(20_000, 100); got != 19_000 {
		t.Fatalf("small-window threshold = %d, want 19000", got)
	}
}

func TestValidateContextCheckpointRequiresRoleContract(t *testing.T) {
	root := testContextCheckpoint(t, "root")
	child := testContextCheckpoint(t, "child")
	if _, err := validateContextCheckpoint(root, "root", 16_000); err != nil {
		t.Fatal(err)
	}
	if _, err := validateContextCheckpoint(child, "child", 16_000); err != nil {
		t.Fatal(err)
	}
	if _, err := validateContextCheckpoint("not a checkpoint", "root", 16_000); err == nil {
		t.Fatal("invalid checkpoint was accepted")
	}
	malformed := strings.Replace(root, "&lt;fact&gt;", "<injected>fact</injected>", 1)
	if _, err := validateContextCheckpoint(malformed, "root", 16_000); err == nil {
		t.Fatal("model-authored nested XML was accepted")
	}
	large := make(map[string]string, len(compactionStagesForTest))
	for _, stage := range compactionStagesForTest {
		large[stage.Name] = strings.Repeat(stage.Name+" evidence ", 2000)
	}
	checkpoint, err := rootprompt.BuildContextCheckpoint("root", large)
	if err != nil {
		t.Fatal(err)
	}
	if provider.EstimateTokens(checkpoint, "model") <= 16_000 {
		t.Fatal("large checkpoint fixture did not exceed the old arbitrary limit")
	}
	if _, err := validateContextCheckpoint(checkpoint, "root", 60_000); err != nil {
		t.Fatalf("checkpoint fitting the actual continuation context was rejected: %v", err)
	}
	if _, err := validateContextCheckpoint(checkpoint, "root", 1_000); err == nil {
		t.Fatal("checkpoint exceeding the continuation context was accepted")
	}
}

func testContextCheckpoint(t *testing.T, role string) string {
	t.Helper()
	sections := make(map[string]string, len(compactionStagesForTest))
	for _, stage := range compactionStagesForTest {
		sections[stage.Name] = "- " + stage.Name + ": exact <fact> & evidence"
	}
	checkpoint, err := rootprompt.BuildContextCheckpoint(role, sections)
	if err != nil {
		t.Fatal(err)
	}
	return checkpoint
}

func TestRetainRecentUserContextExcludesRuntimeData(t *testing.T) {
	human := provider.NewTextMessage("user", "current request")
	runtime := provider.NewTextMessage("user", "<turn_context>memory</turn_context>")
	runtime.Origin = provider.OriginRuntimeContext
	runtime.Visibility = provider.VisibilityContextOnly
	btw := provider.NewTextMessage("user", "correction")
	btw.Origin = provider.OriginBTW
	retained := retainRecentUserContext([]provider.ChatMessage{human, runtime, btw}, "root", 20_000, "model")
	if len(retained) != 2 || retained[0].ContentString() != "current request" || retained[1].ContentString() != "correction" {
		t.Fatalf("retained context = %#v", retained)
	}
}

func TestChildRetentionAlwaysKeepsAssignment(t *testing.T) {
	assignment := provider.NewTextMessage("user", strings.Repeat("assignment", 100))
	assignment.Origin = provider.OriginAssignment
	retained := retainRecentUserContext([]provider.ChatMessage{assignment}, "child", 1, "model")
	if len(retained) != 1 || retained[0].EffectiveOrigin() != provider.OriginAssignment {
		t.Fatalf("child assignment was lost: %#v", retained)
	}
}

func TestCompactUsesSameConversationAndDisablesTools(t *testing.T) {
	requests := make(chan provider.ChatRequest, len(compactionStagesForTest))
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request provider.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		requests <- request
		writeChatSSE(w, fmt.Sprintf("- stage: %d\n- fact: exact", calls.Add(1)), "stop")
	}))
	defer server.Close()
	p := provider.FromPreset("openai-chat", "key", server.URL)
	p.Model = "compact-model"
	provider.SetModelContext(p.Model, 200_000)
	a := newTestAgent(t, p, p.Model)
	human := provider.NewTextMessage("user", "inspect the parser")
	history := compactionTestHistory(human)
	result, err := a.Compact(context.Background(), history, "<system>stable</system>", nil)
	if err != nil {
		t.Fatal(err)
	}
	for range compactionStagesForTest {
		request := <-requests
		if request.ToolChoice != "none" {
			t.Fatalf("tool choice = %#v, want none", request.ToolChoice)
		}
		if len(request.Messages) != len(history)+3 || request.Messages[0].ContentString() != "<system>stable</system>" ||
			!strings.Contains(request.Messages[len(request.Messages)-2].ContentString(), "plain text only") {
			t.Fatalf("compaction request did not preserve the common prefix: messages=%d", len(request.Messages))
		}
	}
	if len(result.Messages) != 3 || result.Messages[0].EffectiveOrigin() != provider.OriginCheckpoint ||
		result.Messages[1].ContentString() != human.ContentString() || result.Messages[2].EffectiveOrigin() != provider.OriginRuntimeContext {
		t.Fatalf("compacted messages = %#v", result.Messages)
	}
	if !strings.Contains(result.Messages[0].ContentString(), `version="3"`) ||
		!strings.Contains(result.Messages[0].ContentString(), "<intent>") {
		t.Fatalf("checkpoint was not deterministically wrapped: %q", result.Messages[0].ContentString())
	}
}

func compactionTestHistory(last provider.ChatMessage) []provider.ChatMessage {
	return []provider.ChatMessage{
		provider.NewTextMessage("assistant", strings.Repeat("old evidence ", 12_000)),
		last,
	}
}

func TestCompactEndsWithFreshContinuationNotHistoricalUserInput(t *testing.T) {
	var calls atomic.Int32
	normalRequests := make(chan provider.ChatRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		call := calls.Add(1)
		if call > int32(len(compactionStagesForTest)) {
			var body provider.ChatRequest
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			normalRequests <- body
			writeChatSSE(w, "Continued only the unfinished work.", "stop")
			return
		}
		// Deliberately omit runtime facts: they cannot depend on model recall.
		writeChatSSE(w, "- previous user request: already answered\n- next: inspect the running build", "stop")
	}))
	defer server.Close()
	p := provider.FromPreset("openai-chat", "key", server.URL)
	p.Model = "continuation-handoff-model"
	provider.SetModelContext(p.Model, 200_000)
	a := newTestAgent(t, p, p.Model)
	a.ContinuationContext = func(context.Context) (rootprompt.SessionContinuityData, error) {
		status := "running"
		if calls.Load() >= int32(len(compactionStagesForTest)) {
			status = "completed"
		}
		return rootprompt.SessionContinuityData{Additional: "latest_human_turn: completed\nasync handle=build-owned status=" + status}, nil
	}
	history := append(compactionTestHistory(provider.NewTextMessage("user", "What did we do?")),
		provider.NewTextMessage("assistant", "The requested answer has already been delivered."))
	result, err := a.Compact(context.Background(), history, "system", nil)
	if err != nil {
		t.Fatal(err)
	}
	last := result.Messages[len(result.Messages)-1]
	if last.IsGenuineUserInput() || last.EffectiveVisibility() != provider.VisibilityContextOnly ||
		!strings.Contains(last.ContentString(), "not a new user request") {
		t.Fatalf("compaction replays old input instead of handing off: %+v", last)
	}
	if !strings.Contains(last.ContentString(), "latest_human_turn: completed") ||
		!strings.Contains(last.ContentString(), "handle=build-owned status=completed") ||
		strings.Contains(last.ContentString(), "handle=build-owned status=running") {
		t.Fatalf("handoff omitted or guessed current runtime state: %q", last.ContentString())
	}
	for _, message := range result.Messages[1:] {
		if message.IsGenuineUserInput() {
			t.Fatalf("answered human request was replayed after compaction: %+v", message)
		}
	}
	if history[len(history)-1].ContentString() != "The requested answer has already been delivered." {
		t.Fatal("compaction changed the original conversation")
	}
	a.ContextCompactionPercent = 0
	a.Run(context.Background(), result.Messages, "system", nil, nil, "", nil)
	request := <-normalRequests
	if len(request.Messages) == 0 || !strings.Contains(request.Messages[len(request.Messages)-1].ContentString(), "<runtime_continuity>") {
		t.Fatalf("post-compaction request has no runtime handoff: %+v", request.Messages)
	}
	continuityCount := 0
	for _, message := range request.Messages[1:] {
		if strings.Contains(message.ContentString(), "<runtime_continuity>") {
			continuityCount++
		}
		if strings.Contains(message.ContentString(), "What did we do?") {
			t.Fatalf("post-compaction request presented an answered prompt as new input: %+v", message)
		}
	}
	if continuityCount != 2 {
		t.Fatalf("post-compaction request retained %d runtime handoffs", continuityCount)
	}
	for i, message := range result.Messages {
		if request.Messages[i+1].ContentString() != message.ContentString() {
			t.Fatal("new turn rewrote the accepted compaction prefix")
		}
	}
}

func TestCompactUsesDurableCompletedTurnWhenAssistantProjectionIsAbsent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeChatSSE(w, "- request already completed; no pending response", "stop")
	}))
	defer server.Close()
	p := provider.FromPreset("openai-chat", "key", server.URL)
	p.Model = "durable-turn-compaction"
	p.SetContextWindow(128_000)
	a := newTestAgent(t, p, p.Model)
	a.LatestHumanTurnState = func(context.Context) (string, bool, error) { return "completed", true, nil }
	human := provider.NewTextMessage("user", "Historical request whose final projection is unavailable\n"+strings.Repeat("resolved detail ", 2000))
	result, err := a.Compact(context.Background(), []provider.ChatMessage{human}, "system", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range result.Messages[1:] {
		if message.IsGenuineUserInput() {
			t.Fatalf("durably completed request was replayed: %+v", result.Messages)
		}
	}
}

func TestCompactRetriesOnlyFailedStage(t *testing.T) {
	var calls atomic.Int32
	var events []AgentEvent
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		call := calls.Add(1)
		if call == 2 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		writeChatSSE(w, fmt.Sprintf("- extraction: %d", call), "stop")
	}))
	defer server.Close()
	p := provider.FromPreset("openai-chat", "key", server.URL)
	p.Model = "compact-model-retry"
	provider.SetModelContext(p.Model, 200_000)
	a := newTestAgent(t, p, p.Model)
	a.compactionRetryDelay = func(int) time.Duration { return 0 }
	human := provider.NewTextMessage("user", "preserve this request")
	if _, err := a.Compact(context.Background(), compactionTestHistory(human), "system", func(event AgentEvent) {
		events = append(events, event)
	}); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != int32(len(compactionStagesForTest)+1) {
		t.Fatalf("requests = %d, want one failed-stage replay", calls.Load())
	}
	if !hasEvent(events, "context_compaction_retrying") {
		t.Fatalf("events lack stage retry: %+v", events)
	}
}

func TestCompactRejectsEmptyStageWithoutResult(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writeChatSSE(w, "", "stop")
	}))
	defer server.Close()
	p := provider.FromPreset("openai-chat", "key", server.URL)
	p.Model = "compact-model-invalid"
	provider.SetModelContext(p.Model, 200_000)
	a := newTestAgent(t, p, p.Model)
	a.compactionRetryDelay = func(int) time.Duration { return 0 }
	if _, err := a.Compact(context.Background(), compactionTestHistory(provider.NewTextMessage("user", "request")), "system", nil); err == nil {
		t.Fatal("empty extraction was accepted")
	}
	if calls.Load() != compactionStageRecoveries+1 {
		t.Fatalf("empty stage attempts=%d want initial + %d recoveries", calls.Load(), compactionStageRecoveries)
	}
}

func TestCompactCancellationDoesNotRetry(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		started <- struct{}{}
		<-release
	}))
	defer server.Close()
	p := provider.FromPreset("openai-chat", "key", server.URL)
	p.Model = "compact-model-cancel"
	provider.SetModelContext(p.Model, 200_000)
	a := newTestAgent(t, p, p.Model)
	a.compactionRetryDelay = func(int) time.Duration { return 0 }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := a.Compact(ctx, compactionTestHistory(provider.NewTextMessage("user", "request")), "system", nil)
		done <- err
	}()
	select {
	case <-started:
		cancel()
		close(release)
	case <-time.After(time.Second):
		t.Fatal("compaction request did not start")
	}
	select {
	case err := <-done:
		if err == nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled compaction did not stop")
	}
	if calls.Load() != 1 {
		t.Fatalf("cancelled compaction retried %d requests", calls.Load())
	}
}

func TestProviderBaselineAboveTwoPercentTriggersAutomaticCompaction(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		call := calls.Add(1)
		if call <= int32(len(compactionStagesForTest)) {
			writeChatSSE(w, fmt.Sprintf("- stage: %d\n- state: retained", call), "stop")
			return
		}
		writeChatSSE(w, "continued", "stop")
	}))
	defer server.Close()
	p := provider.FromPreset("openai-chat", "key", server.URL)
	p.Model = "two-percent-model"
	provider.SetModelContext(p.Model, 1_000_000)
	a := newTestAgent(t, p, p.Model)
	a.ContextCompactionPercent = 2
	// Previous request: system + user; assistant starts at message index 2 in
	// Agent.Run's canonical request. The new user message is a local delta.
	a.SeedContextUsage(43_000, 200, 2)
	messages := []provider.ChatMessage{
		provider.NewTextMessage("user", "previous request"),
		provider.NewTextMessage("assistant", "previous answer"),
		provider.NewTextMessage("user", "next request"),
	}
	var events []AgentEvent
	result := a.Run(context.Background(), messages, "system", func(event AgentEvent) {
		events = append(events, event)
	}, nil, "", nil)
	if calls.Load() != int32(len(compactionStagesForTest)+1) {
		t.Fatalf("provider calls=%d want four compaction stages plus one normal request", calls.Load())
	}
	if len(result) == 0 || !strings.Contains(result[len(result)-1].ContentString(), "continued") {
		t.Fatalf("post-compaction result=%+v", result)
	}
	startedAt, compactedAt, preparingAt := -1, -1, -1
	for index, event := range events {
		switch event.Type {
		case "context_compaction_started":
			if startedAt < 0 {
				startedAt = index
			}
		case "context_compacted":
			if compactedAt < 0 {
				compactedAt = index
			}
		case "phase":
			if preparingAt < 0 {
				preparingAt = index
			}
		}
	}
	if startedAt < 0 || compactedAt <= startedAt || preparingAt <= compactedAt {
		t.Fatalf("ordinary request preparation was not blocked behind compaction: started=%d compacted=%d preparing=%d events=%+v", startedAt, compactedAt, preparingAt, events)
	}
}

func TestChildCompactPreservesAssignmentAndReportContract(t *testing.T) {
	requests := make(chan provider.ChatRequest, len(compactionStagesForTest))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request provider.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		requests <- request
		writeChatSSE(w, "- child assignment: preserved\n- report: required", "stop")
	}))
	defer server.Close()
	p := provider.FromPreset("openai-chat", "key", server.URL)
	p.Model = "child-compact-model"
	provider.SetModelContext(p.Model, 200_000)
	a := newTestAgent(t, p, p.Model)
	a.Depth = 1
	assignment := provider.NewTextMessage("user", "inspect parser and call write_report")
	assignment.Origin = provider.OriginAssignment
	result, err := a.Compact(context.Background(), compactionTestHistory(assignment), "<child_system>stable</child_system>", nil)
	if err != nil {
		t.Fatal(err)
	}
	request := <-requests
	if !strings.Contains(request.Messages[len(request.Messages)-2].ContentString(), "child agent") {
		t.Fatalf("child compaction prompt = %q", request.Messages[len(request.Messages)-2].ContentString())
	}
	if len(result.Messages) != 3 || result.Messages[1].EffectiveOrigin() != provider.OriginAssignment || result.Messages[2].EffectiveOrigin() != provider.OriginRuntimeContext ||
		!strings.Contains(result.Messages[0].ContentString(), "<continuation>") {
		t.Fatalf("child compacted messages = %#v", result.Messages)
	}
}
