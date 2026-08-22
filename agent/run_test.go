package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"crude/provider"
	"crude/tools"
)

func writeChatSSE(w http.ResponseWriter, content, finishReason string) {
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = fmt.Fprintf(w,
		"data: {\"choices\":[{\"delta\":{\"content\":%q},\"finish_reason\":%q}]}\n\n"+
			"data: [DONE]\n\n",
		content, finishReason,
	)
}

func TestRunDoesNotSpinOnClosedBTWChannel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeChatSSE(w, "done", "stop")
	}))
	defer server.Close()

	p := provider.FromPreset("openai-chat", "key", server.URL)
	p.Model = "test-model"
	agent := newTestAgent(t, p, p.Model)
	agent.MaxSteps = 2

	btw := make(chan provider.ChatMessage)
	close(btw)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	var events []AgentEvent
	messages := agent.Run(ctx, nil, "system", func(event AgentEvent) {
		events = append(events, event)
	}, nil, nil, "", btw)
	if ctx.Err() != nil {
		t.Fatal("Run hung while draining a closed BTW channel")
	}
	if len(messages) < 2 {
		t.Fatalf("messages = %d", len(messages))
	}
	if !hasEvent(events, "done") {
		t.Fatalf("events lack done: %+v", events)
	}
}

func TestRunAutomaticallyContinuesTruncatedResponse(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			writeChatSSE(w, "part one", "length")
			return
		}
		writeChatSSE(w, "part two", "stop")
	}))
	defer server.Close()

	p := provider.FromPreset("openai-chat", "key", server.URL)
	p.Model = "test-model"
	agent := newTestAgent(t, p, p.Model)
	agent.MaxSteps = 3

	messages := agent.Run(context.Background(), nil, "system", func(AgentEvent) {}, nil, nil, "", nil)
	if requests.Load() != 2 {
		t.Fatalf("requests = %d, want 2", requests.Load())
	}
	var assistantContent []string
	for _, message := range messages {
		if message.Role == "assistant" {
			assistantContent = append(assistantContent, message.ContentString())
		}
	}
	if len(assistantContent) != 2 || assistantContent[0] != "part one" || assistantContent[1] != "part two" {
		t.Fatalf("assistant messages = %#v", assistantContent)
	}
}

func TestRunAutomaticallyContinuesInterruptedTextStream(t *testing.T) {
	var requests atomic.Int32
	secondRequest := make(chan []provider.ChatMessage, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request provider.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		if requests.Add(1) == 1 {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"part one\"}}]}\n\n")
			return
		}
		secondRequest <- request.Messages
		writeChatSSE(w, " part two", "stop")
	}))
	defer server.Close()

	p := provider.FromPreset("openai-chat", "key", server.URL)
	p.Model = "test-model"
	agent := newTestAgent(t, p, p.Model)
	agent.MaxSteps = 3

	var events []AgentEvent
	messages := agent.Run(context.Background(), nil, "system", func(event AgentEvent) {
		events = append(events, event)
	}, nil, nil, "", nil)
	if requests.Load() != 2 {
		t.Fatalf("requests = %d, want 2", requests.Load())
	}
	if hasEvent(events, "error") {
		t.Fatalf("interrupted text stream surfaced an error: %+v", events)
	}
	if !hasEvent(events, "retry") || !hasEvent(events, "done") {
		t.Fatalf("events lack retry/done recovery markers: %+v", events)
	}

	var streamed strings.Builder
	for _, event := range events {
		if event.Type == "stream_token" {
			if token, ok := event.Data.(string); ok {
				streamed.WriteString(token)
			}
		}
	}
	if got := streamed.String(); got != "part one part two" {
		t.Fatalf("streamed content = %q", got)
	}

	requestMessages := <-secondRequest
	var sawPartial, sawContinuation bool
	for _, message := range requestMessages {
		if message.Role == "assistant" && message.ContentString() == "part one" {
			sawPartial = true
		}
		if message.Role == "system" && strings.Contains(message.ContentString(), "Continue the previous response") {
			sawContinuation = true
		}
	}
	if !sawPartial || !sawContinuation {
		t.Fatalf("recovery request lacks partial assistant/continuation: %+v", requestMessages)
	}

	var assistantContent []string
	for _, message := range messages {
		if message.Role == "assistant" {
			assistantContent = append(assistantContent, message.ContentString())
		}
	}
	if len(assistantContent) != 2 || assistantContent[0] != "part one" || assistantContent[1] != " part two" {
		t.Fatalf("assistant messages = %#v", assistantContent)
	}
}

func TestRunDoesNotContinueInterruptedToolCall(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w,
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"execute_command","arguments":"{\"command\":\"touch /tmp/never-run\""}}]}}]}`+"\n\n")
	}))
	defer server.Close()

	p := provider.FromPreset("openai-chat", "key", server.URL)
	p.Model = "test-model"
	agent := newTestAgent(t, p, p.Model)
	agent.MaxSteps = 3

	var events []AgentEvent
	agent.Run(context.Background(), nil, "system", func(event AgentEvent) {
		events = append(events, event)
	}, nil, nil, "", nil)
	if requests.Load() != 1 {
		t.Fatalf("requests = %d, want no continuation for a partial tool call", requests.Load())
	}
	if !hasEvent(events, "error") {
		t.Fatalf("partial tool call must fail closed: %+v", events)
	}
	if !hasEvent(events, "tool_call_streaming") {
		t.Fatalf("test stream did not decode a partial tool call: %+v", events)
	}
}

func TestRunDoesNotContinueAfterContextWindowExhaustion(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		writeChatSSE(w, "final partial", "model_context_window_exceeded")
	}))
	defer server.Close()

	p := provider.FromPreset("openai-chat", "key", server.URL)
	p.Model = "test-model"
	agent := newTestAgent(t, p, p.Model)
	agent.MaxSteps = 3

	messages := agent.Run(context.Background(), nil, "system", func(AgentEvent) {}, nil, nil, "", nil)
	if requests.Load() != 1 {
		t.Fatalf("requests = %d, want 1", requests.Load())
	}
	if got := messages[len(messages)-1].ContentString(); got != "final partial" {
		t.Fatalf("last message = %q", got)
	}
}

func TestRunAppendsParallelToolResultsInCallOrder(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w,
				"data: {\"choices\":[{\"delta\":{\"tool_calls\":["+
					"{\"index\":0,\"id\":\"call_slow\",\"type\":\"function\",\"function\":{\"name\":\"slow\",\"arguments\":\"{}\"}},"+
					"{\"index\":1,\"id\":\"call_fast\",\"type\":\"function\",\"function\":{\"name\":\"fast\",\"arguments\":\"{}\"}}"+
					"]},\"finish_reason\":\"tool_calls\"}]}\n\n"+
					"data: [DONE]\n\n")
			return
		}
		writeChatSSE(w, "finished", "stop")
	}))
	defer server.Close()

	p := provider.FromPreset("openai-chat", "key", server.URL)
	p.Model = "test-model"
	agent := newTestAgent(t, p, p.Model)
	agent.MaxSteps = 3
	agent.ToolFn = func(ctx context.Context, name string, params map[string]any) (tools.ToolResult, error) {
		if name == "slow" {
			time.Sleep(30 * time.Millisecond)
			return tools.ToolResult{Content: "slow result"}, nil
		}
		return tools.ToolResult{Content: "Error: fast failed"}, nil
	}

	var eventMu sync.Mutex
	emit := func(AgentEvent) {
		eventMu.Lock()
		eventMu.Unlock()
	}
	messages := agent.Run(context.Background(), nil, "system", emit, nil, nil, "", nil)

	var toolMessages []provider.ChatMessage
	for _, message := range messages {
		if message.Role == "tool" {
			toolMessages = append(toolMessages, message)
		}
	}
	if len(toolMessages) != 2 {
		t.Fatalf("tool messages = %d: %+v", len(toolMessages), toolMessages)
	}
	if toolMessages[0].ToolCallID != "call_slow" || toolMessages[1].ToolCallID != "call_fast" {
		t.Fatalf("tool result order = %q, %q", toolMessages[0].ToolCallID, toolMessages[1].ToolCallID)
	}
	if toolMessages[0].ToolError || !toolMessages[1].ToolError {
		t.Fatalf("tool error flags = %v, %v", toolMessages[0].ToolError, toolMessages[1].ToolError)
	}
}

func TestRunPersistsPartialResponseWhenCancelled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-r.Context().Done()
	}))
	defer server.Close()

	p := provider.FromPreset("openai-chat", "key", server.URL)
	p.Model = "test-model"
	agent := newTestAgent(t, p, p.Model)
	ctx, cancel := context.WithCancel(context.Background())
	messages := agent.Run(ctx, nil, "system", func(event AgentEvent) {
		if event.Type == "stream_token" {
			cancel()
		}
	}, nil, nil, "", nil)

	var found bool
	for _, message := range messages {
		if message.Role == "assistant" && message.ContentString() == "partial" {
			found = true
		}
	}
	if !found {
		t.Fatalf("partial assistant response was not persisted: %+v", messages)
	}
}

func TestRunDoesNotExecuteTruncatedToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w,
			"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"dangerous\",\"arguments\":\"{}\"}}]},\"finish_reason\":\"length\"}]}\n\n"+
				"data: [DONE]\n\n")
	}))
	defer server.Close()

	p := provider.FromPreset("openai-chat", "key", server.URL)
	p.Model = "test-model"
	agent := newTestAgent(t, p, p.Model)
	var executions atomic.Int32
	agent.ToolFn = func(context.Context, string, map[string]any) (tools.ToolResult, error) {
		executions.Add(1)
		return tools.ToolResult{Content: "unexpected"}, nil
	}
	var events []AgentEvent
	messages := agent.Run(context.Background(), nil, "system", func(event AgentEvent) {
		events = append(events, event)
	}, nil, nil, "", nil)
	if executions.Load() != 0 {
		t.Fatalf("truncated tool was executed %d times", executions.Load())
	}
	if !hasEvent(events, "error") {
		t.Fatalf("events lack truncation error: %+v", events)
	}
	for _, message := range messages {
		if message.Role == "tool" {
			t.Fatalf("unexpected tool result persisted: %+v", message)
		}
	}
}

func TestRunBalancesToolCallsWhenCancelledDuringExecution(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w,
			"data: {\"choices\":[{\"delta\":{\"tool_calls\":["+
				"{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"slow_one\",\"arguments\":\"{}\"}},"+
				"{\"index\":1,\"id\":\"call_2\",\"type\":\"function\",\"function\":{\"name\":\"slow_two\",\"arguments\":\"{}\"}}"+
				"]},\"finish_reason\":\"tool_calls\"}]}\n\n"+
				"data: [DONE]\n\n")
	}))
	defer server.Close()

	p := provider.FromPreset("openai-chat", "key", server.URL)
	p.Model = "test-model"
	agent := newTestAgent(t, p, p.Model)
	agent.ToolFn = func(context.Context, string, map[string]any) (tools.ToolResult, error) {
		time.Sleep(50 * time.Millisecond)
		return tools.ToolResult{Content: "late result"}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	var eventMu sync.Mutex
	var events []AgentEvent
	messages := agent.Run(ctx, nil, "system", func(event AgentEvent) {
		eventMu.Lock()
		events = append(events, event)
		eventMu.Unlock()
		if event.Type == "tool_call" {
			cancel()
		}
	}, nil, nil, "", nil)

	var toolResults []provider.ChatMessage
	for _, message := range messages {
		if message.Role == "tool" {
			toolResults = append(toolResults, message)
		}
	}
	if len(toolResults) != 2 {
		t.Fatalf("tool results = %d, want one per call: %+v", len(toolResults), toolResults)
	}
	for i, result := range toolResults {
		if result.ToolCallID != fmt.Sprintf("call_%d", i+1) || !result.ToolError {
			t.Fatalf("tool result %d = %+v", i, result)
		}
	}
	time.Sleep(80 * time.Millisecond)
	eventMu.Lock()
	defer eventMu.Unlock()
	terminal := -1
	for index, event := range events {
		if event.Type == "done" || event.Type == "error" {
			terminal = index
			break
		}
	}
	if terminal < 0 {
		t.Fatalf("events lack terminal event: %+v", events)
	}
	if terminal != len(events)-1 {
		t.Fatalf("events arrived after terminal event: %+v", events[terminal+1:])
	}
}

func TestRunRecoversToolPanicsAndContinues(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w,
				"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"panic_tool\",\"arguments\":\"{}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n"+
					"data: [DONE]\n\n")
			return
		}
		writeChatSSE(w, "recovered", "stop")
	}))
	defer server.Close()

	p := provider.FromPreset("openai-chat", "key", server.URL)
	p.Model = "test-model"
	agent := newTestAgent(t, p, p.Model)
	agent.MaxSteps = 3
	agent.ToolFn = func(context.Context, string, map[string]any) (tools.ToolResult, error) {
		panic("boom")
	}
	messages := agent.Run(context.Background(), nil, "system", func(AgentEvent) {}, nil, nil, "", nil)
	if requests.Load() != 2 {
		t.Fatalf("requests = %d, want 2", requests.Load())
	}
	var panicResult *provider.ChatMessage
	for i := range messages {
		if messages[i].Role == "tool" {
			panicResult = &messages[i]
			break
		}
	}
	if panicResult == nil || !panicResult.ToolError ||
		!strings.Contains(panicResult.ContentString(), "panicked") {
		t.Fatalf("panic tool result = %+v", panicResult)
	}
}

func TestFailedWriteReportDoesNotMarkReportComplete(t *testing.T) {
	agent := NewAgent(nil, "test-model", nil)
	agent.ToolFn = func(context.Context, string, map[string]any) (tools.ToolResult, error) {
		return tools.ToolResult{Content: "report path unavailable"}, fmt.Errorf("write failed")
	}
	result := agent.executeOneToolCall(
		context.Background(),
		provider.ToolCall{
			ID: "call_1", InternalID: "_tc1",
			Function: provider.FunctionCall{Name: "write_report", Arguments: `{"report":"details"}`},
		},
		func(AgentEvent) {},
		nil,
		new(string),
	)
	if !result.toolError {
		t.Fatalf("tool result was not marked as an error: %+v", result)
	}
	if agent.writeReportCalled {
		t.Fatal("failed write_report suppressed the mandatory report reminder")
	}
}

func TestDefaultToolDefinitionsValidateRequiredParameters(t *testing.T) {
	agent := newTestAgent(t, nil, "test-model")
	var executed atomic.Bool
	agent.ToolFn = func(context.Context, string, map[string]any) (tools.ToolResult, error) {
		executed.Store(true)
		return tools.ToolResult{Content: "unexpected"}, nil
	}
	result := agent.executeOneToolCall(
		context.Background(),
		provider.ToolCall{
			ID: "call_1", InternalID: "_tc1",
			Function: provider.FunctionCall{Name: "read_file", Arguments: `{}`},
		},
		func(AgentEvent) {},
		nil,
		new(string),
	)
	if executed.Load() {
		t.Fatal("tool executed without its required path")
	}
	if !result.toolError || !strings.Contains(result.result.Content, "path") {
		t.Fatalf("result = %+v", result)
	}
}

func hasEvent(events []AgentEvent, typ string) bool {
	for _, event := range events {
		if event.Type == typ {
			return true
		}
	}
	return false
}
