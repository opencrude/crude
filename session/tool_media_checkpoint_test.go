package session

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"crude/agent"
	"crude/provider"
	"crude/sessiondb"
	"crude/tools"
	recoverytypes "crude/types/recovery"
)

// The MCP/session bridge supplies ordinary ToolResult attachments. Exercise the
// next boundary independently: a finished media leaf must survive the real tool
// collector and durable context publication while a sibling remains in flight.
func TestToolMediaSurvivesParallelCheckpointAndNextRequest(t *testing.T) {
	for _, native := range []bool{false, true} {
		for _, child := range []bool{false, true} {
			t.Run(fmt.Sprintf("native=%t/child=%t", native, child), func(t *testing.T) {
				testToolMediaCheckpoint(t, native, child)
			})
		}
	}
}

func testToolMediaCheckpoint(t *testing.T, native, child bool) {
	t.Helper()
	// A complete one-pixel PNG; assertions compare the exact bytes independently
	// of the projection helpers under test.
	const pngBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+a0WQAAAAASUVORK5CYII="
	imageURL := "data:image/png;base64," + pngBase64
	var requests, effects atomic.Int32
	nextRequest := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode provider request: %v", err)
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if requests.Add(1) > 1 {
			select {
			case nextRequest <- body:
			case <-r.Context().Done():
				return
			}
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"finished\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			return
		}
		var calls []map[string]any
		if native {
			calls = append(calls, map[string]any{"index": 0, "id": "media-batch", "type": "function", "function": map[string]any{
				"name": "multi_tool_use.parallel", "arguments": `{"tool_uses":[{"recipient_name":"functions.read_file","parameters":{"path":"slow"}},{"recipient_name":"functions.read_file","parameters":{"path":"fast"}}]}`,
			}})
		} else {
			for i, path := range []string{"slow", "fast"} {
				calls = append(calls, map[string]any{"index": i, "id": path, "type": "function", "function": map[string]any{
					"name": "read_file", "arguments": fmt.Sprintf(`{"path":%q}`, path),
				}})
			}
		}
		encoded, err := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": calls}, "finish_reason": "tool_calls"}}})
		if err != nil {
			t.Errorf("encode fixture response: %v", err)
			return
		}
		_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", encoded)
	}))
	defer server.Close()

	home := t.TempDir()
	s, err := NewWithSessionDir("", modelAttributionConfig(server.URL, "checkpoint-media"), home, home)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	db, owner := s.db, s.sessionID
	if child {
		db = sessiondb.OpenAndMigrateAt(t.TempDir())
		defer db.Close()
		owner = "child-media-owner"
		if err := sessiondb.CreateSessionRow(db, owner); err != nil {
			t.Fatal(err)
		}
	}
	input := provider.NewTextMessage("user", "preserve screenshot evidence")
	input.InternalID = "media-assignment"
	input = canonicalContextMessage(input)
	state, err := sessiondb.PublishContext(db, sessiondb.ContextPublication{
		SessionID: owner, AgentID: owner,
		Mutation: sessiondb.ContextMutation{Replace: &sessiondb.ContextReplacement{Generation: 1, Reason: "test", Messages: []provider.ChatMessage{input}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !child {
		s.activeContext, s.contextRevision, s.contextGeneration, s.contextReason = state.Messages, state.Revision, 1, "test"
	}
	run := newRecoveryRunState(provider.PlainInput("task"), &sessiondb.RecoveryCheckpoint{
		Archive: state.Messages, ActiveContext: state.Messages, ContextOwnerID: owner,
		ContextRevision: state.Revision, ContextGeneration: 1, ContextReason: "test",
	})
	status := recoverytypes.Status{ID: "media-incident", AgentID: owner, Generation: 1}
	a := agent.NewAgent(provider.FromPreset("openai-chat", "key", server.URL), "checkpoint-media", s.logger)
	a.FilteredTools = append(append([]tools.ToolDef(nil), tools.AgentTools...), tools.ParallelTool())
	a.ContextArchive, a.FrozenPrefix = state.Messages, state.Messages
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseSlow := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseSlow()
	a.ToolFn = func(ctx context.Context, _ string, params map[string]any) (tools.ToolResult, error) {
		effects.Add(1)
		if params["path"] == "slow" {
			select {
			case <-release:
			case <-ctx.Done():
				return tools.ToolResult{}, ctx.Err()
			}
			return tools.ToolResult{Content: "accepted slow"}, nil
		}
		return tools.ToolResult{Content: "accepted fast media", Attachments: []provider.ContentPart{{
			Type: "image_url", MimeType: "image/png", ImageURL: &provider.ImageURL{URL: imageURL},
		}}}, nil
	}
	fastSaved := make(chan struct{}, 1)
	publish := func(archive, active, inFlight []provider.ChatMessage) error {
		if child {
			current, _, err := sessiondb.LoadContextState(db, owner, owner)
			if err != nil {
				return err
			}
			if err := retainChildCheckpointSource(run, archive, active, current); err != nil {
				return err
			}
			run.contextRevision = current.Revision
		} else {
			run.snapshot(archive, active)
		}
		run.inFlightTools = append([]provider.ChatMessage(nil), inFlight...)
		var err error
		if child {
			err = run.publish(db, owner, status)
		} else {
			err = s.publishRootRecovery(run, status)
		}
		if err != nil {
			return err
		}
		for _, msg := range append(append([]provider.ChatMessage(nil), active...), inFlight...) {
			if msg.Role == "tool" && strings.Contains(msg.ContentString(), "accepted fast media") {
				select {
				case fastSaved <- struct{}{}:
				default:
				}
			}
		}
		return nil
	}
	a.Checkpoint = func(archive, active []provider.ChatMessage) error { return publish(archive, active, nil) }
	a.CheckpointTools = publish
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	done := make(chan []provider.ChatMessage, 1)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		done <- a.Run(ctx, state.Messages, "test system", nil, nil, owner, nil)
	}()
	defer func() {
		releaseSlow()
		cancel()
		select {
		case <-stopped:
		case <-time.After(time.Second):
		}
	}()
	select {
	case <-fastSaved:
	case <-ctx.Done():
		t.Fatalf("media sibling did not checkpoint: %v", a.RunFailure())
	}
	checkpoint, found, err := sessiondb.LoadRecoveryCheckpoint(db, owner, owner)
	if err != nil || !found {
		t.Fatalf("load media checkpoint: found=%t err=%v", found, err)
	}
	evidence := checkpoint.ActiveContext
	if native {
		evidence = checkpoint.InFlightTools
	}
	assertCheckpointMediaURL(t, evidence, imageURL)
	// Reconciliation is the passive interruption path: no provider/tool execution
	// is performed to rediscover a result already in durable evidence.
	reconciled := reconcileInterruptedTools(checkpoint.ActiveContext, checkpoint.InFlightTools...)
	assertCheckpointMediaURL(t, reconciled, imageURL)
	if effects.Load() != 2 || requests.Load() != 1 {
		t.Fatalf("passive reconciliation changed execution: effects=%d requests=%d", effects.Load(), requests.Load())
	}
	releaseSlow()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("media tools did not finish")
	}
	if err := a.RunFailure(); err != nil {
		t.Fatalf("media tool completion entered recovery: %v", err)
	}
	var body map[string]any
	select {
	case body = <-nextRequest:
	case <-ctx.Done():
		t.Fatal("next provider request not observed")
	}
	part := findTypedMap(body, "image_url")
	if part == nil || nestedWireString(part, "url") != imageURL {
		t.Fatal("actual provider request lost the exact image attachment")
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(nestedWireString(part, "url"), "data:image/png;base64,"))
	if err != nil || base64.StdEncoding.EncodeToString(decoded) != pngBase64 {
		t.Fatalf("provider image bytes changed: %v", err)
	}
	current, found, err := sessiondb.LoadContextState(db, owner, owner)
	if err != nil || !found {
		t.Fatalf("load final media context: found=%t err=%v", found, err)
	}
	assertCheckpointMediaURL(t, current.Messages, imageURL)
	if requests.Load() != 2 || effects.Load() != 2 {
		t.Fatalf("media tool replayed: requests=%d effects=%d", requests.Load(), effects.Load())
	}
}

func assertCheckpointMediaURL(t *testing.T, messages []provider.ChatMessage, want string) {
	t.Helper()
	for _, msg := range messages {
		if msg.Role != "tool" {
			continue
		}
		for _, part := range msg.Content.Parts() {
			if part.Type == "image_url" && part.ImageURL != nil && part.ImageURL.URL == want {
				return
			}
		}
	}
	t.Fatal("durable tool evidence lost the exact media attachment")
}
