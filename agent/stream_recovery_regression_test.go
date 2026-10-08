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

func TestGatewayStreamDisconnectSupervisedProtocolOwners(t *testing.T) {
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
						const diagnostic = "stream error: stream disconnected before completion: stream closed before response.completed"
						if id == "ollama" {
							_, _ = fmt.Fprintf(w, "{\"error\":%q}\n", diagnostic)
						} else {
							_, _ = fmt.Fprintf(w, "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"invalid_request_error\",\"message\":%q}}\n\n", diagnostic)
						}
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
					"system", emit, nil, "session", nil, SupervisedRunOptions{Supervisor: supervisor, OnAttention: func(RecoveryFailure) AttentionDisposition { return AttentionParkAttempt }})
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
