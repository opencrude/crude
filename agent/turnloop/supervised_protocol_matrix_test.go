package turnloop

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"crude/agent/provider"
	"crude/agent/tools"
	"crude/internal/providersim"
	recoverytypes "crude/types/recovery"
)

func TestRegisteredProtocolsSupervisedPartialToolRecovery(t *testing.T) {
	t.Parallel()
	fixtures := supervisedWires
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
				server := providersim.New(t)
				partial := fixture.interruptedCall(acceptedText, partialID)
				server.Script(partial, providersim.Text("finished"))
				p := provider.FromPreset("openai-chat", "key", server.BaseURL(fixture.wire))
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
				if result.State != SupervisedRunCompleted || len(server.Requests()) != 2 || effects.Load() != 0 {
					t.Fatalf("state=%s requests=%d effects=%d failure=%v", result.State, len(server.Requests()), effects.Load(), result.Failure.Cause)
				}
				if elapsed := time.Since(started); elapsed < 900*time.Millisecond || elapsed > 5*time.Minute {
					t.Fatalf("automatic cooldown elapsed=%s", elapsed)
				}
				second := string(server.Requests()[1].Raw)
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
	server := providersim.New(t)
	server.Script(providersim.Text("accepted").WithCut(2))
	p := provider.FromPreset("openai-chat", "key", server.BaseURL(providersim.Chat))
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
		done <- a.RunSupervised(ctx, []provider.ChatMessage{provider.NewTextMessage("user", "work")},
			"system", emit, nil, "session", nil, SupervisedRunOptions{Supervisor: supervisor})
	}()
	<-waiting
	cancel()
	result := <-done
	if result.State != SupervisedRunCancelled || len(server.Requests()) != 1 {
		t.Fatalf("state=%s requests=%d", result.State, len(server.Requests()))
	}
}

func TestRegisteredProtocolsPermanentFailuresParkWithoutRetry(t *testing.T) {
	for _, protocol := range provider.ProtocolList() {
		id := protocol.Info().ID
		for _, failure := range []struct {
			name string
			kind providersim.FaultKind
		}{{"schema", providersim.InvalidRequest}, {"auth", providersim.Auth}, {"quota", providersim.Quota}} {
			label := failure.name
			if id == "ollama" && failure.kind == providersim.Quota {
				label = "gateway_quota"
			}
			t.Run(id+"/"+label, func(t *testing.T) {
				t.Parallel()
				server := providersim.New(t)
				wire := supervisedWires[id].wire
				base := server.BaseURL(wire)
				if wire == providersim.Ollama && failure.kind == providersim.Quota {
					// Native quota is not applicable: https://docs.ollama.com/api/errors
					// documents rate limiting only. This is a gateway billing rejection.
					server.Script(providersim.HTTPError(providersim.RateLimit))
					base = translatingGateway(t, server, wire, func(r *http.Response) error {
						if err := r.Body.Close(); err != nil {
							return err
						}
						gatewayBody(r, `{"error":{"code":"insufficient_quota","message":"billing limit"}}`)
						return nil
					})
				} else {
					server.Script(providersim.HTTPError(failure.kind))
				}
				p := provider.FromPreset("openai-chat", "key", base)
				if err := p.SetProtocolID(id); err != nil {
					t.Fatal(err)
				}
				p.Model = "negative-" + id
				p.SetDialSettings(provider.DialSettings{MaxAttempts: 1})
				a := newTestAgent(t, p, p.Model)
				supervisor := NewSupervisor("root", 9, nil)
				// A permanent verdict must not enter a cooldown. End a mistaken retry
				// immediately rather than issuing another paid model request.
				supervisor.Wait = func(context.Context, time.Duration) error {
					t.Error("permanent failure scheduled a retry")
					return context.Canceled
				}
				supervisor.Persist = func(recoverytypes.Status) error { return nil }
				result := a.RunSupervised(t.Context(), []provider.ChatMessage{provider.NewTextMessage("user", "ORIGINAL-negative-"+id)},
					"system", nil, nil, "session", nil, SupervisedRunOptions{
						Supervisor:  supervisor,
						OnAttention: func(RecoveryFailure) AttentionDisposition { return AttentionParkAttempt },
					})
				if result.State != SupervisedRunParked || result.Incident.State != recoverytypes.NeedsAttention ||
					!result.Incident.RetryAt.IsZero() || len(server.Requests()) != 1 {
					t.Fatalf("state=%s incident=%+v requests=%d failure=%v", result.State, result.Incident, len(server.Requests()), result.Failure.Cause)
				}
			})
		}
	}
}

// Frame counts through the incomplete call, after the accepted text. On
// object-valued wires the call itself is atomic; its enclosing turn is not.
type supervisedWire struct {
	wire          providersim.Wire
	partialFrames int
}

var supervisedWires = map[string]supervisedWire{
	"openai":              {providersim.Chat, 4},
	"openai-responses":    {providersim.Responses, 10},
	"codex":               {providersim.Codex, 10},
	"anthropic":           {providersim.Messages, 6},
	"gemini":              {providersim.Gemini, 2},
	"gemini-interactions": {providersim.Interactions, 5},
	"ollama":              {providersim.Ollama, 2},
}

func (f supervisedWire) interruptedCall(accepted, id string) providersim.Turn {
	args := `{"path":"partial.go"}`
	if f.wire == providersim.Chat || f.wire == providersim.Codex || f.wire == providersim.Responses {
		args = `{"path":`
	}
	return providersim.Text(accepted).WithTools(providersim.ToolCall{ID: id, Name: "read_file", Arguments: args}).WithCut(f.partialFrames)
}
