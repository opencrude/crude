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
	recoverytypes "crude/types/recovery"
)

func TestRegisteredProtocolsSupervisedPartialToolRecovery(t *testing.T) {
	t.Parallel()
	fixtures := supervisedProtocolFixtures()
	protocols := provider.ProtocolList()
	if len(fixtures) != len(protocols) {
		t.Fatalf("fixtures=%d registered protocols=%d", len(fixtures), len(protocols))
	}
	for _, protocol := range protocols {
		id := protocol.Info().ID
		fixture, ok := fixtures[id]
		if !ok {
			t.Fatalf("missing supervised fixture for registered protocol %q", id)
		}
		for _, owner := range []struct {
			name, id string
			depth    int
		}{{"root", "root-owner", 0}, {"child", "child-owner", 1}} {
			t.Run(id+"/"+owner.name, func(t *testing.T) {
				t.Parallel()
				original := "ORIGINAL-" + id + "-" + owner.name
				acceptedText := "ACCEPTED-" + id + "-" + owner.name
				partialID := "incomplete-" + id + "-" + owner.name
				var requests atomic.Int32
				var bodiesMu sync.Mutex
				var bodies [][]byte
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var raw json.RawMessage
					if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
						t.Errorf("decode request: %v", err)
						return
					}
					bodiesMu.Lock()
					bodies = append(bodies, append([]byte(nil), raw...))
					bodiesMu.Unlock()
					w.Header().Set("Content-Type", fixture.contentType)
					if requests.Add(1) == 1 {
						_, _ = fmt.Fprint(w, fixture.partial(acceptedText, partialID))
						return
					}
					_, _ = fmt.Fprint(w, fixture.complete)
				}))
				defer server.Close()

				p := provider.FromPreset("openai-chat", "key", server.URL)
				if err := p.SetProtocolID(id); err != nil {
					t.Fatal(err)
				}
				p.Model = "supervised-" + id
				p.SetDialSettings(provider.DialSettings{MaxAttempts: 1})
				a := newTestAgent(t, p, p.Model)
				a.ContextCompactionPercent = 0
				a.AgentID, a.Depth = AgentID(owner.id), owner.depth
				if owner.depth > 0 {
					a.ParentID, a.reportPublished = "root-owner", true
				}
				a.FilteredTools = FilterTools(tools.AgentTools, []string{"read_file"}, nil, "", owner.depth > 0)
				var effects atomic.Int32
				a.ToolFn = func(context.Context, string, map[string]any) (tools.ToolResult, error) {
					effects.Add(1)
					return tools.ToolResult{}, nil
				}
				supervisor := NewSupervisor(owner.id, 7, nil)
				var persisted atomic.Int32
				var recovering recoverytypes.Status
				supervisor.Persist = func(status recoverytypes.Status) error {
					if status.State == recoverytypes.Recovering && !status.RetryAt.IsZero() {
						persisted.Add(1)
					}
					return nil
				}
				emit := func(event AgentEvent) {
					if status, ok := event.Data.(recoverytypes.Status); event.Type == "recovery" && ok && status.State == recoverytypes.Recovering {
						if !status.RetryAt.IsZero() {
							recovering = status
						}
					}
				}
				supervisor.Emit = emit
				started := time.Now()
				result := a.RunSupervised(t.Context(), []provider.ChatMessage{provider.NewTextMessage("user", original)},
					"system", emit, nil, "session", nil, SupervisedRunOptions{Supervisor: supervisor})
				if result.State != SupervisedRunCompleted || requests.Load() != 2 || effects.Load() != 0 {
					t.Fatalf("state=%s requests=%d effects=%d failure=%v", result.State, requests.Load(), effects.Load(), result.Failure.Cause)
				}
				if elapsed := time.Since(started); elapsed < 900*time.Millisecond || elapsed > 5*time.Minute {
					t.Fatalf("automatic cooldown elapsed=%s", elapsed)
				}
				bodiesMu.Lock()
				second := string(bodies[1])
				bodiesMu.Unlock()
				if strings.Count(second, original) != 1 || strings.Count(second, acceptedText) != 1 ||
					!strings.Contains(second, "runtime_recovery") || strings.Contains(second, partialID) || strings.Contains(second, "previous_response_id") {
					t.Fatalf("unsafe or incomplete semantic replacement: %s", second)
				}
				if recovering.AgentID != owner.id || recovering.Generation != 7 || recovering.RetryAt.IsZero() ||
					!recovering.RetryAt.After(recovering.UpdatedAt) || persisted.Load() == 0 {
					t.Fatalf("missing persisted exact-owner countdown: %+v persisted=%d", recovering, persisted.Load())
				}
				accepted, finished := 0, 0
				for _, message := range result.Archive {
					if strings.Contains(message.ContentString()+message.ReasoningContent, acceptedText) {
						accepted++
					}
					if message.ContentString() == "finished" {
						finished++
					}
					if len(message.ToolCalls) != 0 {
						t.Fatalf("partial call became executable: %+v", message.ToolCalls)
					}
				}
				if accepted != 1 || finished != 1 {
					t.Fatalf("accepted=%d finished=%d archive=%+v", accepted, finished, result.Archive)
				}
			})
		}
	}
}

func TestSupervisedPartialContinuationStopsDuringCooldown(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"accepted\"}}]}\n\n")
	}))
	defer server.Close()
	p := provider.FromPreset("openai-chat", "key", server.URL)
	p.SetDialSettings(provider.DialSettings{MaxAttempts: 1})
	a := newTestAgent(t, p, "stop-cooldown")
	ctx, cancel := context.WithCancel(t.Context())
	supervisor := NewSupervisor("root", 1, nil)
	supervisor.Persist = func(recoverytypes.Status) error { return nil }
	waiting := make(chan struct{})
	var once sync.Once
	done := make(chan SupervisedRunResult, 1)
	emit := func(event AgentEvent) {
		if event.Type == "recovery" {
			if status, ok := event.Data.(recoverytypes.Status); ok && status.State == recoverytypes.Recovering && !status.RetryAt.IsZero() {
				once.Do(func() { close(waiting) })
			}
		}
	}
	supervisor.Emit = emit
	go func() {
		done <- a.RunSupervised(ctx, []provider.ChatMessage{provider.NewTextMessage("user", "work")}, "system", emit, nil, "session", nil, SupervisedRunOptions{Supervisor: supervisor})
	}()
	<-waiting
	cancel()
	result := <-done
	if result.State != SupervisedRunCancelled || requests.Load() != 1 {
		t.Fatalf("state=%s requests=%d", result.State, requests.Load())
	}
}

func TestRegisteredProtocolsPermanentFailuresParkWithoutRetry(t *testing.T) {
	for _, protocol := range provider.ProtocolList() {
		id := protocol.Info().ID
		for _, failure := range []struct {
			name, body string
			status     int
		}{
			{"schema", `{"error":{"type":"invalid_request_error","param":"tools[0]","message":"invalid tool schema"}}`, http.StatusBadRequest},
			{"auth", `{"error":{"type":"authentication_error","message":"invalid credential"}}`, http.StatusUnauthorized},
			{"quota", `{"error":{"code":"insufficient_quota","message":"billing limit"}}`, http.StatusTooManyRequests},
		} {
			t.Run(id+"/"+failure.name, func(t *testing.T) {
				t.Parallel()
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					requests.Add(1)
					w.WriteHeader(failure.status)
					_, _ = fmt.Fprint(w, failure.body)
				}))
				defer server.Close()
				p := provider.FromPreset("openai-chat", "key", server.URL)
				if err := p.SetProtocolID(id); err != nil {
					t.Fatal(err)
				}
				p.Model = "negative-" + id
				p.SetDialSettings(provider.DialSettings{MaxAttempts: 1})
				a := newTestAgent(t, p, p.Model)
				supervisor := NewSupervisor("root", 9, nil)
				supervisor.Persist = func(recoverytypes.Status) error { return nil }
				result := a.RunSupervised(t.Context(), []provider.ChatMessage{provider.NewTextMessage("user", "ORIGINAL-negative-"+id)},
					"system", nil, nil, "session", nil, SupervisedRunOptions{
						Supervisor:  supervisor,
						OnAttention: func(RecoveryFailure) AttentionDisposition { return AttentionParkAttempt },
					})
				if result.State != SupervisedRunParked || result.Incident.State != recoverytypes.NeedsAttention ||
					!result.Incident.RetryAt.IsZero() || requests.Load() != 1 {
					t.Fatalf("state=%s incident=%+v requests=%d failure=%v", result.State, result.Incident, requests.Load(), result.Failure.Cause)
				}
			})
		}
	}
}

type supervisedProtocolFixture struct {
	contentType string
	partial     func(accepted, partialID string) string
	complete    string
}

func supervisedProtocolFixtures() map[string]supervisedProtocolFixture {
	responsesPartial := func(accepted, partialID string) string {
		return fmt.Sprintf("data: {\"type\":\"response.output_text.delta\",\"delta\":%q}\n\ndata: {\"type\":\"response.output_item.added\",\"output_index\":1,\"item\":{\"id\":%q,\"type\":\"function_call\",\"call_id\":%q,\"name\":\"read_file\",\"arguments\":\"\"}}\n\ndata: {\"type\":\"response.function_call_arguments.delta\",\"output_index\":1,\"delta\":\"{\\\"path\\\":\"}\n\n", accepted, partialID, partialID)
	}
	responsesComplete := "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"finished\"}]}]}}\n\n"
	return map[string]supervisedProtocolFixture{
		"openai": {"text/event-stream", func(a, id string) string {
			return fmt.Sprintf("data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\ndata: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":%q,\"type\":\"function\",\"function\":{\"name\":\"read_file\",\"arguments\":\"{\\\"path\\\":\"}}]}}]}\n\n", a, id)
		}, "data: {\"choices\":[{\"delta\":{\"content\":\"finished\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"},
		"openai-responses": {"text/event-stream", responsesPartial, responsesComplete},
		"codex":            {"text/event-stream", responsesPartial, responsesComplete},
		"anthropic": {"text/event-stream", func(a, id string) string {
			return fmt.Sprintf("data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":%q}}\n\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"tool_use\",\"id\":%q,\"name\":\"read_file\",\"input\":{}}}\n\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"path\\\":\"}}\n\n", a, id)
		}, "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"finished\"}}\n\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n"},
		"ollama": {"application/x-ndjson", func(a, id string) string {
			return fmt.Sprintf("{\"model\":\"m\",\"message\":{\"role\":\"assistant\",\"content\":%q}}\n{\"model\":\"m\",\"message\":{\"role\":\"assistant\",\"content\":\"\",\"tool_calls\":[{\"id\":%q,\"function\":{\"name\":\"read_file\",\"arguments\":\"{\\\"path\\\":\"}}]}}\n", a, id)
		}, "{\"model\":\"m\",\"message\":{\"role\":\"assistant\",\"content\":\"finished\"},\"done\":true,\"done_reason\":\"stop\"}\n"},
	}
}
