package turnloop

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"crude/agent/provider"
	"crude/agent/tools"
	"crude/internal/providersim"
	"crude/types/toolresult"
)

type agentRoundTripFunc func(*http.Request) (*http.Response, error)

func (f agentRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type eventThenErrorBody struct {
	payload []byte
	offset  int
	err     error
}

func (b *eventThenErrorBody) Read(dst []byte) (int, error) {
	if b.offset < len(b.payload) {
		n := copy(dst, b.payload[b.offset:])
		b.offset += n
		return n, nil
	}
	return 0, b.err
}

func (b *eventThenErrorBody) Close() error { return nil }

func TestRunDoesNotSpinOnClosedRuntimeChannel(t *testing.T) {
	server := providersim.New(t)
	server.Script(providersim.Text("done"))

	p := provider.FromPreset("openai-chat", "key", server.BaseURL(providersim.Chat))
	p.Model = "test-model"
	agent := newTestAgent(t, p, p.Model)

	runtimeMessages := make(chan RuntimeMessage)
	close(runtimeMessages)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	var events []AgentEvent
	messages := agent.Run(ctx, nil, "system", func(event AgentEvent) {
		events = append(events, event)
	}, nil, "", runtimeMessages)
	if ctx.Err() != nil {
		t.Fatal("Run hung while draining a closed runtime channel")
	}
	if len(messages) < 2 {
		t.Fatalf("messages = %d", len(messages))
	}
	if !hasEvent(events, "done") {
		t.Fatalf("events lack done: %+v", events)
	}
}

func TestRunRestartsUploadToCoalesceQueuedHumanSteering(t *testing.T) {
	var requests atomic.Int32
	firstUploadPaused := make(chan struct{})
	secondMessages := make(chan []provider.ChatMessage, 1)
	transport := agentRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		if requests.Add(1) == 1 {
			partialBytes := request.ContentLength / 2
			if partialBytes < 1 {
				partialBytes = 1
			}
			if _, err := io.CopyN(io.Discard, request.Body, partialBytes); err != nil {
				return nil, fmt.Errorf("read partial request body: %w", err)
			}
			close(firstUploadPaused)
			<-request.Context().Done()
			return nil, request.Context().Err()
		}

		body, err := io.ReadAll(request.Body)
		if err != nil {
			return nil, fmt.Errorf("read restarted request body: %w", err)
		}
		var chatRequest provider.ChatRequest
		if err := json.Unmarshal(body, &chatRequest); err != nil {
			return nil, fmt.Errorf("decode restarted request: %w", err)
		}
		secondMessages <- chatRequest.Messages
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body: io.NopCloser(strings.NewReader(
				"data: {\"choices\":[{\"delta\":{\"content\":\"combined response\"},\"finish_reason\":\"stop\"}]}\n\n" +
					"data: [DONE]\n\n",
			)),
			Request: request,
		}, nil
	})

	p := provider.FromPreset("openai-chat", "key", "https://runtime-upload.invalid")
	p.SetDialSettings(provider.DialSettings{MaxAttempts: 1})
	p.Model = "test-model"
	agent := newTestAgent(t, p, p.Model)
	agent.testHTTPClient = &http.Client{Transport: transport}

	runtimeMessages := make(chan RuntimeMessage, 4)
	var admissions atomic.Int32
	var events []AgentEvent
	done := make(chan []provider.ChatMessage, 1)
	go func() {
		done <- agent.Run(
			context.Background(),
			[]provider.ChatMessage{provider.NewTextMessage("user", strings.Repeat("initial context ", 256))},
			"system",
			func(event AgentEvent) { events = append(events, event) },
			nil,
			"",
			runtimeMessages,
		)
	}()

	select {
	case <-firstUploadPaused:
	case <-time.After(2 * time.Second):
		t.Fatal("first upload did not reach the restartable range")
	}
	first := provider.NewTextMessage("user", "first queued correction")
	first.InternalID = "steering-restart-1"
	second := provider.NewTextMessage("user", "second queued correction")
	second.InternalID = "steering-restart-2"
	runtimeMessages <- RuntimeMessage{Message: first, Admit: func([]provider.ChatMessage) error {
		admissions.Add(1)
		return nil
	}}
	runtimeMessages <- RuntimeMessage{Message: second, Admit: func([]provider.ChatMessage) error {
		admissions.Add(1)
		return nil
	}}

	var archive []provider.ChatMessage
	select {
	case archive = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("agent did not finish restarted human-steering upload")
	}
	if requests.Load() != 2 {
		t.Fatalf("requests = %d, want one interrupted upload and one restarted upload", requests.Load())
	}
	if admissions.Load() != 2 {
		t.Fatalf("human-steering admissions = %d, want 2", admissions.Load())
	}

	requestMessages := <-secondMessages
	requestCounts := map[string]int{}
	for _, message := range requestMessages {
		if message.Role == "assistant" {
			t.Fatalf("interrupted upload created an assistant message: %#v", message)
		}
		switch message.ContentString() {
		case first.ContentString():
			requestCounts[first.InternalID]++
		case second.ContentString():
			requestCounts[second.InternalID]++
		}
	}
	if requestCounts[first.InternalID] != 1 || requestCounts[second.InternalID] != 1 {
		t.Fatalf("restarted request steering counts = %#v", requestCounts)
	}

	archiveCounts := map[string]int{}
	for _, message := range archive {
		archiveCounts[message.InternalID]++
	}
	if archiveCounts[first.InternalID] != 1 || archiveCounts[second.InternalID] != 1 {
		t.Fatalf("archive steering counts = %#v", archiveCounts)
	}

	successfulRequests := 0
	for _, event := range events {
		switch event.Type {
		case "error", "retry":
			t.Fatalf("restart emitted unexpected %s event: %#v", event.Type, event)
		case "done":
			data, _ := event.Data.(map[string]any)
			if data["reason"] == "cancelled" {
				t.Fatalf("internal upload restart became a turn cancellation: %#v", event)
			}
		case "usage":
			data, _ := event.Data.(map[string]any)
			if data["request_outcome"] == "succeeded" {
				successfulRequests++
			}
		}
	}
	if successfulRequests != 1 {
		t.Fatalf("successful request accounting = %d, want only the restarted request", successfulRequests)
	}
}

func TestRunContinuesWithHumanSteeringArrivingDuringFinalCompletion(t *testing.T) {
	first, second := providersim.NewGate(), providersim.NewGate()
	server := providersim.New(t)
	server.Script(providersim.Text("first response").WithGate(first), providersim.Text("second response").WithGate(second))
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	p := provider.FromPreset("openai-chat", "key", server.BaseURL(providersim.Chat))
	p.Model = "test-model"
	agent := newTestAgent(t, p, p.Model)
	runtimeMessages := make(chan RuntimeMessage, 1)
	var admissions atomic.Int32
	events := make(chan AgentEvent, 32)
	done := make(chan []provider.ChatMessage, 1)
	go func() {
		done <- agent.Run(ctx, nil, "system", func(event AgentEvent) {
			events <- event
		}, nil, "", runtimeMessages)
	}()

	if err := first.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	steeringMessage := provider.NewTextMessage("user", "use this in the next turn")
	steeringMessage.InternalID = "steering-1"
	runtimeMessages <- RuntimeMessage{Message: steeringMessage, Admit: func([]provider.ChatMessage) error {
		admissions.Add(1)
		return nil
	}}
	first.Release()
	if err := second.Wait(ctx); err != nil {
		t.Fatal(err)
	}

	var observed []AgentEvent
	drainEvents := func() {
		for {
			select {
			case event := <-events:
				observed = append(observed, event)
			default:
				return
			}
		}
	}
	drainEvents()
	if admissions.Load() != 1 {
		t.Fatal("human steering was not admitted before the next request")
	}
	// The second provider response is still blocked. The handoff belongs to
	// the upload, independently of the model's first output.
	for _, event := range observed {
		if event.Type == "stream_token" && event.Data == "second response" {
			t.Fatal("second model output arrived before the response was released")
		}
	}
	second.Release()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("agent did not finish human-steering continuation")
	}
	if len(server.Requests()) != 2 {
		t.Fatalf("requests=%d want human steering to force a second request", len(server.Requests()))
	}
	drainEvents()
	successful := map[string]bool{}
	for _, event := range observed {
		if event.Type == "usage" {
			data := event.Data.(map[string]any)
			if data["request_outcome"] == "succeeded" {
				successful[data["request_id"].(string)] = true
			}
		}
	}
	if len(successful) != 2 {
		t.Fatalf("one actor turn made two successful API calls, accounted %+v", successful)
	}
	requestMessages := server.Requests()[1].Messages
	foundSteering := false
	for _, message := range requestMessages {
		if message.Role == "user" && strings.Contains(simulatedText(message.Parts), "use this in the next turn") {
			foundSteering = true
			break
		}
	}
	if !foundSteering {
		t.Fatalf("second request lacks human steering: %+v", requestMessages)
	}
}

func TestRunRetainsAdmittedHumanSteeringWhenProviderRejectsRequest(t *testing.T) {
	server := providersim.New(t)
	server.Script(providersim.HTTPError(providersim.InvalidRequest))

	p := provider.FromPreset("openai-chat", "key", server.BaseURL(providersim.Chat))
	p.Model = "test-model"
	agent := newTestAgent(t, p, p.Model)
	steeringMessage := provider.NewTextMessage("user", "persist once admitted")
	steeringMessage.InternalID = "steering-rejected-request"
	runtimeMessages := make(chan RuntimeMessage, 1)
	var admitted, released atomic.Int32
	runtimeMessages <- RuntimeMessage{
		Message: steeringMessage,
		Admit: func([]provider.ChatMessage) error {
			admitted.Add(1)
			return nil
		},
		Release: func(string) { released.Add(1) },
	}

	var events []AgentEvent
	messages := agent.Run(context.Background(), nil, "system", func(event AgentEvent) {
		events = append(events, event)
	}, nil, "", runtimeMessages)
	if admitted.Load() != 1 || released.Load() != 0 {
		t.Fatalf("admission lifecycle = admitted %d released %d", admitted.Load(), released.Load())
	}
	found := false
	for _, message := range messages {
		if message.InternalID == steeringMessage.InternalID {
			found = true
		}
	}
	if !found {
		t.Fatal("durably admitted human steering was erased after provider rejection")
	}
}

func TestRunRetainsAdmittedHumanSteeringWhenCancelledDuringUpload(t *testing.T) {
	p := provider.FromPreset("openai-chat", "key", "https://example.invalid/v1")
	p.Model = "test-model"
	agent := newTestAgent(t, p, p.Model)
	requestUploaded := make(chan struct{})
	agent.testHTTPClient = &http.Client{Transport: agentRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if _, err := io.Copy(io.Discard, req.Body); err != nil {
			return nil, err
		}
		close(requestUploaded)
		<-req.Context().Done()
		return nil, req.Context().Err()
	})}
	steeringMessage := provider.NewTextMessage("user", "retain if cancelled")
	steeringMessage.InternalID = "steering-cancelled"
	runtimeMessages := make(chan RuntimeMessage, 1)
	var admitted, released atomic.Int32
	runtimeMessages <- RuntimeMessage{
		Message: steeringMessage,
		Admit: func([]provider.ChatMessage) error {
			admitted.Add(1)
			return nil
		},
		Release: func(string) { released.Add(1) },
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var eventsMu sync.Mutex
	var events []AgentEvent
	done := make(chan []provider.ChatMessage, 1)
	go func() {
		done <- agent.Run(ctx, nil, "system", func(event AgentEvent) {
			eventsMu.Lock()
			events = append(events, event)
			eventsMu.Unlock()
		}, nil, "", runtimeMessages)
	}()
	select {
	case <-requestUploaded:
	case <-time.After(2 * time.Second):
		t.Fatal("request upload did not start")
	}
	if admitted.Load() != 1 {
		t.Fatal("human steering was not admitted before upload")
	}
	cancel()
	select {
	case messages := <-done:
		found := false
		for _, message := range messages {
			if message.InternalID == steeringMessage.InternalID {
				found = true
			}
		}
		if !found {
			t.Fatal("cancel erased durably admitted human steering")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled request did not stop")
	}
	if released.Load() != 0 {
		t.Fatalf("admitted steering was released on cancel: %d", released.Load())
	}
}

func TestRunAutomaticallyContinuesTruncatedResponse(t *testing.T) {
	server := providersim.New(t)
	server.Script(providersim.Text("part one").WithFinish(providersim.FinishLength), providersim.Text("part two"))

	p := provider.FromPreset("openai-chat", "key", server.BaseURL(providersim.Chat))
	p.Model = "test-model"
	agent := newTestAgent(t, p, p.Model)

	messages := agent.Run(context.Background(), nil, "system", func(AgentEvent) {}, nil, "", nil)
	if len(server.Requests()) != 2 {
		t.Fatalf("requests = %d, want 2", len(server.Requests()))
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

	server := providersim.New(t)
	server.Script(providersim.Text("part one").WithCut(2), providersim.Text(" part two"))

	p := provider.FromPreset("openai-chat", "key", server.BaseURL(providersim.Chat))
	p.Model = "test-model"
	agent := newTestAgent(t, p, p.Model)

	var events []AgentEvent
	messages := agent.Run(context.Background(), nil, "system", func(event AgentEvent) {
		events = append(events, event)
	}, nil, "", nil)
	if len(server.Requests()) != 2 {
		t.Fatalf("requests = %d, want 2", len(server.Requests()))
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

	requestMessages := server.Requests()[1].Messages
	var sawPartial, sawContinuation bool
	for _, message := range requestMessages {
		if message.Role == "assistant" && simulatedText(message.Parts) == "part one" {
			sawPartial = true
		}
		if message.Role == "user" && strings.HasPrefix(simulatedText(message.Parts), "<runtime_continuation ") &&
			strings.Contains(simulatedText(message.Parts), "Continue the previous response") {
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

func TestRunRecoversInterruptedToolCallWithoutExecuting(t *testing.T) {
	var toolExecutions atomic.Int32
	server := providersim.New(t)
	server.Script(providersim.CallTools(providersim.ToolCall{ID: "call_1", Name: "execute_command", Arguments: `{"command":"touch /tmp/never-run"`}).WithCut(3), providersim.Text("Recovered safely."))

	p := provider.FromPreset("openai-chat", "key", server.BaseURL(providersim.Chat))
	p.Model = "test-model"
	agent := newTestAgent(t, p, p.Model)
	agent.ToolFn = func(context.Context, string, map[string]any) (tools.ToolResult, error) {
		toolExecutions.Add(1)
		return tools.ToolResult{}, nil
	}

	var events []AgentEvent
	agent.Run(context.Background(), nil, "system", func(event AgentEvent) {
		events = append(events, event)
	}, nil, "", nil)
	if len(server.Requests()) != 2 || toolExecutions.Load() != 0 {
		t.Fatalf("requests = %d, tool executions = %d", len(server.Requests()), toolExecutions.Load())
	}
	if hasEvent(events, "error") || !hasEvent(events, "retry") || !hasEvent(events, "done") {
		t.Fatalf("partial tool call did not recover autonomously: %+v", events)
	}
	if !hasEvent(events, "tool_call_streaming") {
		t.Fatalf("test stream did not decode a partial tool call: %+v", events)
	}
}

func TestRunDoesNotContinueAfterContextWindowExhaustion(t *testing.T) {
	server := providersim.New(t)
	server.Script(providersim.Text("final partial").WithFinish("model_context_window_exceeded"))

	p := provider.FromPreset("openai-chat", "key", server.BaseURL(providersim.Chat))
	p.Model = "test-model"
	agent := newTestAgent(t, p, p.Model)

	messages := agent.Run(context.Background(), nil, "system", func(AgentEvent) {}, nil, "", nil)
	if len(server.Requests()) != 1 {
		t.Fatalf("requests = %d, want 1", len(server.Requests()))
	}
	if got := messages[len(messages)-1].ContentString(); got != "final partial" {
		t.Fatalf("last message = %q", got)
	}
}

func TestRunAppendsParallelToolResultsInCallOrder(t *testing.T) {
	server := providersim.New(t)
	server.Script(providersim.CallTools(providersim.ToolCall{ID: "call_slow", Name: "slow", Arguments: `{}`}, providersim.ToolCall{ID: "call_fast", Name: "fast", Arguments: `{}`}), providersim.Text("finished"))

	p := provider.FromPreset("openai-chat", "key", server.BaseURL(providersim.Chat))
	p.Model = "test-model"
	agent := newTestAgent(t, p, p.Model)
	agent.ToolFn = func(ctx context.Context, name string, params map[string]any) (tools.ToolResult, error) {
		if name == "slow" {
			time.Sleep(30 * time.Millisecond)
			return tools.ToolResult{Content: "slow result"}, nil
		}
		return tools.ToolResult{Content: "Error: fast failed"}, nil
	}

	emit := func(AgentEvent) {}
	messages := agent.Run(context.Background(), nil, "system", emit, nil, "", nil)

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
	server := providersim.New(t)
	server.Script(providersim.Text("partial").WithGateAfter(2, providersim.NewGate()))

	p := provider.FromPreset("openai-chat", "key", server.BaseURL(providersim.Chat))
	p.Model = "test-model"
	agent := newTestAgent(t, p, p.Model)
	ctx, cancel := context.WithCancel(context.Background())
	messages := agent.Run(ctx, nil, "system", func(event AgentEvent) {
		if event.Type == "stream_token" {
			cancel()
		}
	}, nil, "", nil)

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

func TestRunContinuesAcceptedPartialDownloadStall(t *testing.T) {
	var calls atomic.Int32
	var requestBodies [][]byte
	transport := agentRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		requestBodies = append(requestBodies, body)
		response := &http.Response{
			StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Request: request,
		}
		if calls.Add(1) == 1 {
			response.Body = &eventThenErrorBody{
				payload: []byte("data: {\"choices\":[{\"delta\":{\"content\":\"partial output\"}}]}\n\n"),
				err:     &provider.ProgressStallError{Phase: "download", Bytes: 494141, Silence: 45 * time.Second},
			}
			return response, nil
		}
		response.Body = io.NopCloser(strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\" continued\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
		return response, nil
	})
	p := provider.FromPreset("openai-chat", "key", "https://stall.invalid")
	p.SetDialSettings(provider.DialSettings{MaxAttempts: 1})
	p.Model = "stall-continuation-model"
	provider.SetModelContext(p.Model, 200_000)
	a := newTestAgent(t, p, p.Model)
	a.testHTTPClient = &http.Client{Transport: transport}
	var events []AgentEvent
	archive := a.Run(context.Background(), []provider.ChatMessage{provider.NewTextMessage("user", "do the work")}, "system", func(event AgentEvent) {
		events = append(events, event)
	}, nil, "", nil)
	if calls.Load() != 2 || len(requestBodies) != 2 {
		t.Fatalf("requests = %d bodies=%d, want initial plus continuation", calls.Load(), len(requestBodies))
	}
	if !strings.Contains(string(requestBodies[1]), "partial output") || !strings.Contains(string(requestBodies[1]), "runtime_continuation") {
		t.Fatalf("continuation request did not preserve partial prefix: %s", requestBodies[1])
	}
	var partial, continuation, completed bool
	for _, message := range archive {
		partial = partial || message.Role == "assistant" && message.ContentString() == "partial output"
		continuation = continuation || message.EffectiveOrigin() == provider.OriginContinuation
		completed = completed || message.Role == "assistant" && message.ContentString() == " continued"
	}
	if !partial || !continuation || !completed {
		t.Fatalf("archive missing autonomous recovery: %#v", archive)
	}
	for _, event := range events {
		if event.Type == "error" {
			t.Fatalf("recoverable download stall emitted terminal error: %#v", event)
		}
	}
}

func TestRunRecoversPartialToolCallWithoutExecutingIt(t *testing.T) {
	var calls atomic.Int32
	var toolExecutions atomic.Int32
	var requestBodies [][]byte
	transport := agentRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		requestBodies = append(requestBodies, body)
		response := &http.Response{
			StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Request: request,
		}
		if calls.Add(1) == 1 {
			response.Body = &eventThenErrorBody{
				payload: []byte("data: {\"choices\":[{\"delta\":{\"content\":\"I will inspect.\",\"tool_calls\":[{\"index\":0,\"id\":\"call_partial\",\"type\":\"function\",\"function\":{\"name\":\"read_file\",\"arguments\":\"{\\\"path\\\":\"}}]}}]}\n\n"),
				err:     &provider.ProgressStallError{Phase: "download", Bytes: 9000, Silence: 45 * time.Second},
			}
			return response, nil
		}
		response.Body = io.NopCloser(strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"Recovered safely.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
		return response, nil
	})
	p := provider.FromPreset("openai-chat", "key", "https://stall.invalid")
	p.SetDialSettings(provider.DialSettings{MaxAttempts: 1})
	p.Model = "partial-tool-recovery-model"
	provider.SetModelContext(p.Model, 200_000)
	a := newTestAgent(t, p, p.Model)
	a.testHTTPClient = &http.Client{Transport: transport}
	a.ToolFn = func(context.Context, string, map[string]any) (tools.ToolResult, error) {
		toolExecutions.Add(1)
		return tools.ToolResult{}, nil
	}
	var emittedError bool
	var discardedTool bool
	archive := a.Run(context.Background(), []provider.ChatMessage{provider.NewTextMessage("user", "inspect")}, "system", func(event AgentEvent) {
		emittedError = emittedError || event.Type == "error"
		if event.Type == "tool_result" {
			data, _ := event.Data.(map[string]any)
			discardedTool = data["tool_id"] == "call_partial" && data["error"] == "true"
		}
	}, nil, "", nil)
	if calls.Load() != 2 || toolExecutions.Load() != 0 || emittedError || !discardedTool {
		t.Fatalf("requests=%d tool executions=%d emitted error=%v discarded=%v", calls.Load(), toolExecutions.Load(), emittedError, discardedTool)
	}
	second := string(requestBodies[1])
	if !strings.Contains(second, "I will inspect.") || !strings.Contains(second, "partial_tool_call") ||
		strings.Contains(second, "call_partial") {
		t.Fatalf("unsafe partial tool state in continuation request: %s", second)
	}
	var recovered bool
	for _, message := range archive {
		recovered = recovered || message.Role == "assistant" && message.ContentString() == "Recovered safely."
	}
	if !recovered {
		t.Fatalf("recovery response missing from archive: %#v", archive)
	}
}

func TestRunDoesNotExecuteTruncatedToolCalls(t *testing.T) {
	server := providersim.New(t)
	server.Respond(func(r providersim.Request) providersim.Turn {
		return providersim.CallTools(providersim.ToolCall{ID: "call_1", Name: "dangerous", Arguments: `{}`}).WithFinish(providersim.FinishLength)
	})

	p := provider.FromPreset("openai-chat", "key", server.BaseURL(providersim.Chat))
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
	}, nil, "", nil)
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
	server := providersim.New(t)
	server.Script(providersim.CallTools(providersim.ToolCall{ID: "call_1", Name: "slow_one", Arguments: `{}`}, providersim.ToolCall{ID: "call_2", Name: "slow_two", Arguments: `{}`}))

	p := provider.FromPreset("openai-chat", "key", server.BaseURL(providersim.Chat))
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
	}, nil, "", nil)

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
		cancelled := result.ToolError && strings.Contains(result.Content.String(), "cancelled before tool execution")
		completed := !result.ToolError && result.Content.String() == "late result"
		if result.ToolCallID != fmt.Sprintf("call_%d", i+1) || (!cancelled && !completed) {
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

// A tool panic is an ordinary tool error with uncertain effects: the loop
// continues and the model inspects state, never a parked run awaiting a human.
func TestRunContainsToolPanicAndContinuesTheLoop(t *testing.T) {
	server := providersim.New(t)
	server.Script(providersim.CallTools(providersim.ToolCall{ID: "call_1", Name: "panic_tool", Arguments: `{}`}), providersim.Text("recovered"))

	p := provider.FromPreset("openai-chat", "key", server.BaseURL(providersim.Chat))
	p.Model = "test-model"
	agent := newTestAgent(t, p, p.Model)
	agent.ToolFn = func(context.Context, string, map[string]any) (tools.ToolResult, error) {
		panic("boom")
	}
	messages := agent.Run(context.Background(), nil, "system", func(AgentEvent) {}, nil, "", nil)
	if len(server.Requests()) != 2 || agent.RunFailure() != nil {
		t.Fatalf("requests = %d, failure=%v; want the loop to continue after the panic", len(server.Requests()), agent.RunFailure())
	}
	var panicResult *provider.ChatMessage
	for i := range messages {
		if messages[i].Role == "tool" {
			panicResult = &messages[i]
			break
		}
	}
	if panicResult == nil || !panicResult.ToolError || panicResult.ToolOutcome == nil ||
		panicResult.ToolOutcome.State != toolresult.Uncertain || !strings.Contains(panicResult.ContentString(), "panicked") || !strings.Contains(panicResult.ContentString(), "Do not replay an operation that may have committed") {
		t.Fatalf("panic tool result = %+v", panicResult)
	}
}

func TestRunAppendsToolRoundWithoutSyntheticRuntimeMessages(t *testing.T) {
	server := providersim.New(t)
	server.Script(providersim.CallTools(providersim.ToolCall{ID: "call_1", Name: "read_file", Arguments: `{"path":"main.go"}`}), providersim.Text("finished"))

	p := provider.FromPreset("openai-chat", "key", server.BaseURL(providersim.Chat))
	p.Model = "test-model"
	agent := newTestAgent(t, p, p.Model)
	agent.ToolFn = func(context.Context, string, map[string]any) (tools.ToolResult, error) {
		return tools.ToolResult{Content: "contents"}, nil
	}

	agent.Run(context.Background(), []provider.ChatMessage{{Role: "user", Content: provider.TextContent("start")}}, "stable system", func(AgentEvent) {}, nil, "session", nil)
	log := server.Requests()
	var captured [][]json.RawMessage
	for _, request := range log {
		var wire struct{ Messages []json.RawMessage }
		if err := json.Unmarshal(request.Raw, &wire); err != nil {
			t.Fatal(err)
		}
		captured = append(captured, wire.Messages)
	}
	if len(captured) != 2 {
		t.Fatalf("request count = %d, want 2", len(captured))
	}
	for _, request := range log {
		if strings.Contains(conversationText(request.Conversation), "## Runtime Status") {
			t.Fatalf("agent injected per-step runtime context: %#v", request.Raw)
		}
	}
	second := captured[1]
	if len(second) <= len(captured[0]) {
		t.Fatalf("second request did not append to first: %#v", second)
	}
	for i := range captured[0] {
		firstJSON, secondJSON := captured[0][i], second[i]
		if string(firstJSON) != string(secondJSON) {
			t.Fatalf("wire prefix mutated at message %d\nfirst=%s\nsecond=%s", i, firstJSON, secondJSON)
		}
	}
	if log[1].Messages[len(log[0].Messages)].Role != "assistant" {
		t.Fatalf("tool exchange did not extend the accepted prefix: %#v", second)
	}
}

func TestUncompactedUserTurnsKeepPriorRequestAsExactPrefix(t *testing.T) {
	server := providersim.New(t)
	server.Script(providersim.Text("answer"), providersim.Text("answer"))
	p := provider.FromPreset("openai-chat", "key", server.BaseURL(providersim.Chat))
	p.Model = "prefix-model"
	a := newTestAgent(t, p, p.Model)
	a.ContextCompactionPercent = 0
	first := a.Run(context.Background(), []provider.ChatMessage{
		provider.NewTextMessage("user", "first request\n\n<turn_context>remembered once</turn_context>"),
	}, "stable system", func(AgentEvent) {}, nil, "", nil)
	secondInput := append(append([]provider.ChatMessage(nil), first[1:]...),
		provider.NewTextMessage("user", "second request\n\n<turn_context>new recall</turn_context>"))
	_ = a.Run(context.Background(), secondInput, "stable system", func(AgentEvent) {}, nil, "", nil)
	log := server.Requests()
	var captured [][]json.RawMessage
	for _, request := range log {
		var wire struct{ Messages []json.RawMessage }
		if err := json.Unmarshal(request.Raw, &wire); err != nil {
			t.Fatal(err)
		}
		captured = append(captured, wire.Messages)
	}
	if len(captured) != 2 || len(captured[1]) <= len(captured[0]) {
		t.Fatalf("captured requests = %#v", captured)
	}
	for i := range captured[0] {
		firstJSON, secondJSON := captured[0][i], captured[1][i]
		if string(firstJSON) != string(secondJSON) {
			t.Fatalf("prior turn changed at message %d\nfirst=%s\nsecond=%s", i, firstJSON, secondJSON)
		}
	}
}

func TestRunHasNoStepBudget(t *testing.T) {
	const toolCycles = 25

	server := providersim.New(t)
	server.Respond(func(r providersim.Request) providersim.Turn {
		if r.Index <= toolCycles {
			return providersim.CallTools(providersim.ToolCall{ID: fmt.Sprintf("call_%d", r.Index), Name: "progress_probe", Arguments: fmt.Sprintf(`{"cycle":%d}`, r.Index)})
		}
		return providersim.Text("all cycles complete")
	})

	p := provider.FromPreset("openai-chat", "key", server.BaseURL(providersim.Chat))
	p.Model = "test-model"
	a := newTestAgent(t, p, p.Model)
	a.ToolFn = func(_ context.Context, _ string, params map[string]any) (tools.ToolResult, error) {
		return tools.ToolResult{Content: fmt.Sprintf("cycle %v complete", params["cycle"])}, nil
	}
	var events []AgentEvent
	messages := a.Run(context.Background(), nil, "system", func(event AgentEvent) {
		events = append(events, event)
	}, nil, "", nil)

	if got := len(server.Requests()); got != toolCycles+1 {
		t.Fatalf("requests=%d want %d; agent stopped before work completed", got, toolCycles+1)
	}
	if !hasEvent(events, "done") || hasEvent(events, "error") {
		t.Fatalf("terminal events = %+v", events)
	}
	if got := messages[len(messages)-1].ContentString(); got != "all cycles complete" {
		t.Fatalf("last message = %q", got)
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
			Function: provider.FunctionCall{Name: "publish_report", Arguments: `{"report":"details"}`},
		},
		func(AgentEvent) {},
		nil,
		new(string),
	)
	if !result.toolError {
		t.Fatalf("tool result was not marked as an error: %+v", result)
	}
	if agent.reportPublished {
		t.Fatal("failed publish_report suppressed the mandatory report reminder")
	}
}

func TestMissingFinalReportGetsOneBoundedReminder(t *testing.T) {
	server := providersim.New(t)
	server.Script(providersim.Text("report omitted"), providersim.Text("report omitted"))

	p := provider.FromPreset("openai-chat", "key", server.BaseURL(providersim.Chat))
	p.Model = "test-model"
	child := newTestAgent(t, p, p.Model)
	child.Depth = 1
	child.FilteredTools = append(append([]tools.ToolDef(nil), tools.AgentTools...), tools.CollaborationTools...)

	var events []AgentEvent
	messages := child.Run(context.Background(), nil, "system", func(event AgentEvent) {
		events = append(events, event)
	}, nil, "", nil)

	if got := len(server.Requests()); got != maxFinalReportReminders+1 {
		t.Fatalf("requests=%d want %d; missing report retried without a bound", got, maxFinalReportReminders+1)
	}
	reminders := 0
	for _, message := range messages {
		if strings.Contains(message.ContentString(), "runtime_report_reminder") {
			reminders++
		}
	}
	if reminders != maxFinalReportReminders {
		t.Fatalf("report reminders=%d want %d", reminders, maxFinalReportReminders)
	}
	for _, event := range events {
		if event.Type != "done" {
			continue
		}
		data, _ := event.Data.(map[string]any)
		if data["reason"] == "missing_final_report" {
			return
		}
	}
	t.Fatalf("terminal events did not report the final-report contract failure: %+v", events)
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

func TestIdenticalEditIsRejectedBeforeDispatcher(t *testing.T) {
	a := newTestAgent(t, nil, "test-model")
	var executed atomic.Bool
	a.ToolFn = func(context.Context, string, map[string]any) (tools.ToolResult, error) {
		executed.Store(true)
		return tools.ToolResult{Content: "unexpected"}, nil
	}
	result := a.executeOneToolCall(context.Background(), provider.ToolCall{
		ID: "no-op", InternalID: "no-op-message", Function: provider.FunctionCall{
			Name: "edit_file", Arguments: `{"path":"x.go","old_string":"unchanged","new_string":"unchanged"}`,
		},
	}, func(AgentEvent) {}, nil, new(string))
	if executed.Load() || !result.toolError || !strings.Contains(result.result.Content, "old_string and new_string are exactly the same") {
		t.Fatalf("identical edit reached dispatcher or was reported as success: %+v", result)
	}
}

func TestFilteredToolListIsEnforcedAtExecution(t *testing.T) {
	agent := newTestAgent(t, nil, "test-model")
	agent.FilteredTools = FilterTools(tools.AgentTools, []string{"read_file"}, nil, "", false)
	var executed atomic.Bool
	agent.ToolFn = func(context.Context, string, map[string]any) (tools.ToolResult, error) {
		executed.Store(true)
		return tools.ToolResult{Content: "unexpected"}, nil
	}
	result := agent.executeOneToolCall(context.Background(), provider.ToolCall{
		ID: "call_denied", InternalID: "_denied", Function: provider.FunctionCall{Name: "write_file", Arguments: `{"path":"x","content":"x"}`},
	}, func(AgentEvent) {}, nil, new(string))
	if executed.Load() {
		t.Fatal("unadvertised tool executed despite the runtime allowlist")
	}
	if !result.toolError || !strings.Contains(result.result.Content, "not available") {
		t.Fatalf("result=%+v", result)
	}
}

func TestCancellationDoesNotStartQueuedBlockingMutation(t *testing.T) {
	agent := newTestAgent(t, nil, "test-model")
	agent.Yolo = true
	started := make(chan struct{})
	var wrote atomic.Bool
	agent.ToolFn = func(ctx context.Context, tool string, _ map[string]any) (tools.ToolResult, error) {
		if tool == "execute_command" {
			close(started)
			<-ctx.Done()
			return tools.ToolResult{Content: "cancelled"}, ctx.Err()
		}
		wrote.Store(true)
		return tools.ToolResult{Content: "wrote"}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	results := agent.executeToolCalls(ctx, []provider.ToolCall{
		{ID: "command", Function: provider.FunctionCall{Name: "execute_command", Arguments: `{"command":"wait"}`}},
		{ID: "write", Function: provider.FunctionCall{Name: "write_file", Arguments: `{"path":"x","content":"x"}`}},
	}, func(AgentEvent) {}, nil, new(string), 1)
	<-started
	cancel()
	<-results
	<-results
	if wrote.Load() {
		t.Fatal("queued write started after cancellation")
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

// BeforeFinish buys exactly one more step per run, so an owner's end-of-turn
// reconciliation can never loop.
func TestBeforeFinishAddsOneStepPerRun(t *testing.T) {
	server := providersim.New(t)
	server.Script(providersim.Text("final answer"), providersim.Text("final answer"))
	p := provider.FromPreset("openai-chat", "key", server.BaseURL(providersim.Chat))
	p.Model = "test-model"
	a := newTestAgent(t, p, p.Model)
	calls := 0
	a.BeforeFinish = func() []provider.ChatMessage {
		calls++
		return []provider.ChatMessage{provider.NewTextMessage("user", "<runtime_plan_reminder>reconcile</runtime_plan_reminder>")}
	}
	messages := a.Run(context.Background(), nil, "system", func(AgentEvent) {}, nil, "", nil)
	if got := len(server.Requests()); got != 2 || calls != 1 {
		t.Fatalf("requests=%d BeforeFinish calls=%d; want 2 and 1", got, calls)
	}
	found := false
	for _, message := range messages {
		found = found || strings.Contains(message.ContentString(), "runtime_plan_reminder")
	}
	if !found {
		t.Fatal("the BeforeFinish notice did not reach the model context")
	}
}

func TestTerminalSteeringSealDrainsLastAcceptedInputBeforeDone(t *testing.T) {
	server := providersim.New(t)
	server.Script(providersim.Text("accepted answer"), providersim.Text("accepted answer"))
	p := provider.FromPreset("openai-chat", "key", server.BaseURL(providersim.Chat))
	p.Model = "terminal-steering-model"
	a := newTestAgent(t, p, p.Model)
	inbox := make(chan RuntimeMessage, 1)
	seals, reopened := 0, 0
	a.SealRuntimeMessages = func(context.Context) error {
		seals++
		if seals == 1 {
			note := provider.NewTextMessage("user", "last accepted human correction")
			note.InternalID = "last-note"
			inbox <- RuntimeMessage{Message: note}
		}
		return nil
	}
	a.ResumeRuntimeMessages = func() { reopened++ }
	done := 0
	messages := a.Run(context.Background(), nil, "system", func(event AgentEvent) {
		if event.Type == "done" {
			done++
		}
	}, nil, "", inbox)
	count := 0
	for _, message := range messages {
		if message.InternalID == "last-note" {
			count++
		}
	}
	if len(server.Requests()) != 2 || done != 1 || seals != 2 || reopened != 1 || count != 1 {
		t.Fatalf("terminal admission stranded/replayed steering: requests=%d done=%d seals=%d reopened=%d notes=%d", len(server.Requests()), done, seals, reopened, count)
	}
}
