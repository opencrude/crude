package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeChatSSE(w, "done", "stop")
	}))
	defer server.Close()

	p := provider.FromPreset("openai-chat", "key", server.URL)
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
	var requests atomic.Int32
	firstStarted := make(chan struct{}, 1)
	releaseFirst := make(chan struct{})
	secondStarted := make(chan struct{}, 1)
	releaseSecond := make(chan struct{})
	secondMessages := make(chan []provider.ChatMessage, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request provider.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		if requests.Add(1) == 1 {
			firstStarted <- struct{}{}
			<-releaseFirst
			writeChatSSE(w, "first response", "stop")
			return
		}
		secondMessages <- request.Messages
		secondStarted <- struct{}{}
		<-releaseSecond
		writeChatSSE(w, "second response", "stop")
	}))
	defer server.Close()

	p := provider.FromPreset("openai-chat", "key", server.URL)
	p.Model = "test-model"
	agent := newTestAgent(t, p, p.Model)
	runtimeMessages := make(chan RuntimeMessage, 1)
	var admissions atomic.Int32
	events := make(chan AgentEvent, 32)
	done := make(chan []provider.ChatMessage, 1)
	go func() {
		done <- agent.Run(context.Background(), nil, "system", func(event AgentEvent) {
			events <- event
		}, nil, "", runtimeMessages)
	}()

	<-firstStarted
	steeringMessage := provider.NewTextMessage("user", "use this in the next turn")
	steeringMessage.InternalID = "steering-1"
	runtimeMessages <- RuntimeMessage{Message: steeringMessage, Admit: func([]provider.ChatMessage) error {
		admissions.Add(1)
		return nil
	}}
	close(releaseFirst)
	<-secondStarted

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
	close(releaseSecond)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("agent did not finish human-steering continuation")
	}
	if requests.Load() != 2 {
		t.Fatalf("requests=%d want human steering to force a second request", requests.Load())
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
	requestMessages := <-secondMessages
	foundSteering := false
	for _, message := range requestMessages {
		if message.Role == "user" && strings.Contains(message.ContentString(), "use this in the next turn") {
			foundSteering = true
			break
		}
	}
	if !foundSteering {
		t.Fatalf("second request lacks human steering: %+v", requestMessages)
	}
}

func TestRunRetainsAdmittedHumanSteeringWhenProviderRejectsRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "invalid request", http.StatusBadRequest)
	}))
	defer server.Close()

	p := provider.FromPreset("openai-chat", "key", server.URL)
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

	messages := agent.Run(context.Background(), nil, "system", func(AgentEvent) {}, nil, "", nil)
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

	var events []AgentEvent
	messages := agent.Run(context.Background(), nil, "system", func(event AgentEvent) {
		events = append(events, event)
	}, nil, "", nil)
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
		if message.Role == "user" && strings.HasPrefix(message.ContentString(), "<runtime_continuation ") &&
			strings.Contains(message.ContentString(), "Continue the previous response") {
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
	var requests atomic.Int32
	var toolExecutions atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) > 1 {
			writeChatSSE(w, "Recovered safely.", "stop")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w,
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"execute_command","arguments":"{\"command\":\"touch /tmp/never-run\""}}]}}]}`+"\n\n")
	}))
	defer server.Close()

	p := provider.FromPreset("openai-chat", "key", server.URL)
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
	if requests.Load() != 2 || toolExecutions.Load() != 0 {
		t.Fatalf("requests = %d, tool executions = %d", requests.Load(), toolExecutions.Load())
	}
	if hasEvent(events, "error") || !hasEvent(events, "retry") || !hasEvent(events, "done") {
		t.Fatalf("partial tool call did not recover autonomously: %+v", events)
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

	messages := agent.Run(context.Background(), nil, "system", func(AgentEvent) {}, nil, "", nil)
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
	agent.ToolFn = func(context.Context, string, map[string]any) (tools.ToolResult, error) {
		panic("boom")
	}
	messages := agent.Run(context.Background(), nil, "system", func(AgentEvent) {}, nil, "", nil)
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

func TestRunAppendsToolRoundWithoutSyntheticRuntimeMessages(t *testing.T) {
	var requestCount atomic.Int32
	requests := make(chan []provider.ChatMessage, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request provider.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		requests <- request.Messages
		if requestCount.Add(1) == 1 {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w,
				"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"read_file\",\"arguments\":\"{\\\"path\\\":\\\"main.go\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n"+
					"data: [DONE]\n\n")
			return
		}
		writeChatSSE(w, "finished", "stop")
	}))
	defer server.Close()

	p := provider.FromPreset("openai-chat", "key", server.URL)
	p.Model = "test-model"
	agent := newTestAgent(t, p, p.Model)
	agent.ToolFn = func(context.Context, string, map[string]any) (tools.ToolResult, error) {
		return tools.ToolResult{Content: "contents"}, nil
	}

	agent.Run(context.Background(), []provider.ChatMessage{{Role: "user", Content: provider.TextContent("start")}}, "stable system", func(AgentEvent) {}, nil, "session", nil)
	close(requests)

	var captured [][]provider.ChatMessage
	for request := range requests {
		captured = append(captured, request)
	}
	if len(captured) != 2 {
		t.Fatalf("request count = %d, want 2", len(captured))
	}
	for _, request := range captured {
		for _, message := range request {
			if strings.Contains(message.ContentString(), "## Runtime Status") {
				t.Fatalf("agent injected per-step runtime context: %#v", request)
			}
		}
	}
	second := captured[1]
	if len(second) <= len(captured[0]) {
		t.Fatalf("second request did not append to first: %#v", second)
	}
	for i := range captured[0] {
		firstJSON, _ := json.Marshal(captured[0][i])
		secondJSON, _ := json.Marshal(second[i])
		if string(firstJSON) != string(secondJSON) {
			t.Fatalf("wire prefix mutated at message %d\nfirst=%s\nsecond=%s", i, firstJSON, secondJSON)
		}
	}
	if second[len(captured[0])].Role != "assistant" {
		t.Fatalf("tool exchange did not extend the accepted prefix: %#v", second)
	}
}

func TestUncompactedUserTurnsKeepPriorRequestAsExactPrefix(t *testing.T) {
	requests := make(chan []provider.ChatMessage, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request provider.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		requests <- request.Messages
		writeChatSSE(w, "answer", "stop")
	}))
	defer server.Close()
	p := provider.FromPreset("openai-chat", "key", server.URL)
	p.Model = "prefix-model"
	a := newTestAgent(t, p, p.Model)
	a.ContextCompactionPercent = 0
	first := a.Run(context.Background(), []provider.ChatMessage{
		provider.NewTextMessage("user", "first request\n\n<turn_context>remembered once</turn_context>"),
	}, "stable system", func(AgentEvent) {}, nil, "", nil)
	secondInput := append(append([]provider.ChatMessage(nil), first[1:]...),
		provider.NewTextMessage("user", "second request\n\n<turn_context>new recall</turn_context>"))
	_ = a.Run(context.Background(), secondInput, "stable system", func(AgentEvent) {}, nil, "", nil)
	close(requests)
	var captured [][]provider.ChatMessage
	for request := range requests {
		captured = append(captured, request)
	}
	if len(captured) != 2 || len(captured[1]) <= len(captured[0]) {
		t.Fatalf("captured requests = %#v", captured)
	}
	for i := range captured[0] {
		firstJSON, _ := json.Marshal(captured[0][i])
		secondJSON, _ := json.Marshal(captured[1][i])
		if string(firstJSON) != string(secondJSON) {
			t.Fatalf("prior turn changed at message %d\nfirst=%s\nsecond=%s", i, firstJSON, secondJSON)
		}
	}
}

func TestRunHasNoStepBudget(t *testing.T) {
	const toolCycles = 25
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		request := int(requests.Add(1))
		if request <= toolCycles {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprintf(w,
				"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_%d\",\"type\":\"function\",\"function\":{\"name\":\"progress_probe\",\"arguments\":\"{\\\"cycle\\\":%d}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n"+
					"data: [DONE]\n\n", request, request)
			return
		}
		writeChatSSE(w, "all cycles complete", "stop")
	}))
	defer server.Close()

	p := provider.FromPreset("openai-chat", "key", server.URL)
	p.Model = "test-model"
	a := newTestAgent(t, p, p.Model)
	a.ToolFn = func(_ context.Context, _ string, params map[string]any) (tools.ToolResult, error) {
		return tools.ToolResult{Content: fmt.Sprintf("cycle %v complete", params["cycle"])}, nil
	}
	var events []AgentEvent
	messages := a.Run(context.Background(), nil, "system", func(event AgentEvent) {
		events = append(events, event)
	}, nil, "", nil)

	if got := requests.Load(); got != toolCycles+1 {
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
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		writeChatSSE(w, "report omitted", "stop")
	}))
	defer server.Close()

	p := provider.FromPreset("openai-chat", "key", server.URL)
	p.Model = "test-model"
	child := newTestAgent(t, p, p.Model)
	child.Depth = 1

	var events []AgentEvent
	messages := child.Run(context.Background(), nil, "system", func(event AgentEvent) {
		events = append(events, event)
	}, nil, "", nil)

	if got := requests.Load(); got != maxFinalReportReminders+1 {
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
