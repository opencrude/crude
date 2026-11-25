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
	"crude/internal/retry"
	recoverytypes "crude/types/recovery"
)

func TestSupervisorUsesBoundedServerCooldownAndStableIncident(t *testing.T) {
	now := time.Unix(1700000000, 0)
	detail := strings.Repeat("upstream diagnostic ", 40)
	statusErr := &provider.StatusError{
		StatusCode: http.StatusServiceUnavailable,
		Message:    detail,
		RetryAfter: 20 * time.Minute,
	}
	var waited time.Duration
	var persisted, emitted []recoverytypes.Status
	supervisor := NewSupervisor("agent-1", 9, nil)
	supervisor.Now = func() time.Time { return now }
	supervisor.Wait = func(ctx context.Context, delay time.Duration) error {
		waited = delay
		return ctx.Err()
	}
	supervisor.Persist = func(status recoverytypes.Status) error {
		persisted = append(persisted, status)
		return nil
	}
	supervisor.Emit = func(event AgentEvent) {
		status, ok := event.Data.(recoverytypes.Status)
		if !ok || event.Type != "recovery" {
			t.Fatalf("event = %#v", event)
		}
		emitted = append(emitted, status)
	}
	if !supervisor.Await(t.Context(), TransientRecovery(statusErr)) {
		t.Fatal("bounded automatic recovery did not continue")
	}
	if waited != retry.MaxCooldown {
		t.Fatalf("wait=%s want=%s", waited, retry.MaxCooldown)
	}
	if len(persisted) != 2 || len(emitted) != 2 {
		t.Fatalf("transitions persisted=%d emitted=%d", len(persisted), len(emitted))
	}
	recovering := emitted[0]
	if recovering.ID == "" || recovering.State != recoverytypes.Recovering ||
		recovering.AgentID != "agent-1" || recovering.Generation != 9 ||
		recovering.Detail != statusErr.Error() ||
		!recovering.RetryAt.Equal(now.Add(retry.MaxCooldown)) ||
		recovering.StartedAt != now || recovering.UpdatedAt != now {
		t.Fatalf("recovering status = %+v", recovering)
	}
	if err := supervisor.Resolve(); err != nil {
		t.Fatal(err)
	}
	if !emitted[1].RetryAt.IsZero() {
		t.Fatal("elapsed cooldown still advertised a future retry")
	}
	if len(emitted) != 3 || emitted[2].ID != recovering.ID ||
		emitted[2].State != recoverytypes.Recovered ||
		emitted[2].Summary != "Run recovered" {
		t.Fatalf("recovered status = %+v", emitted)
	}
}

func TestSupervisorPermanentFailureWaitsForExplicitWake(t *testing.T) {
	wake := make(chan struct{}, 1)
	persisted := make(chan recoverytypes.Status, 4)
	supervisor := NewSupervisor("agent-2", 3, nil)
	supervisor.Wake = wake
	supervisor.Persist = func(status recoverytypes.Status) error {
		persisted <- status
		return nil
	}
	done := make(chan bool, 1)
	go func() {
		done <- supervisor.Await(t.Context(), AttentionRecovery(
			RecoveryFailureConfiguration,
			&provider.StatusError{StatusCode: http.StatusUnauthorized, Message: "bad credentials"},
		))
	}()
	first := <-persisted
	if first.State != recoverytypes.NeedsAttention ||
		!strings.Contains(first.Action, "Settings → Accounts") {
		t.Fatalf("attention status = %+v", first)
	}
	select {
	case <-done:
		t.Fatal("permanent failure retried without an explicit wake")
	default:
	}
	wake <- struct{}{}
	second := <-persisted
	if second.State != recoverytypes.Recovering || second.ID != first.ID {
		t.Fatalf("wake transition = %+v", second)
	}
	if !<-done {
		t.Fatal("explicit wake did not release the parked run")
	}
	if err := supervisor.Cancel(); err != nil {
		t.Fatal(err)
	}
	cancelled := <-persisted
	if cancelled.State != recoverytypes.Cancelled || cancelled.ID != first.ID ||
		cancelled.Action != "" || !cancelled.RetryAt.IsZero() ||
		cancelled.Detail != first.Detail {
		t.Fatalf("cancelled transition = %+v", cancelled)
	}
}

func TestRunSupervisedRetriesTransientExhaustionWithoutTerminalError(t *testing.T) {
	server := providersim.New(t)
	server.Script(providersim.HTTPError(providersim.Overload), providersim.Text("finished after recovery"))

	p := provider.FromPreset("openai-chat", "key", server.BaseURL(providersim.Chat))
	p.SetDialSettings(provider.DialSettings{MaxAttempts: 1})
	a := newTestAgent(t, p, "supervised-model")
	a.ContextCompactionPercent = 0
	supervisor := NewSupervisor("root", 1, nil)
	supervisor.RetryBase = 0
	supervisor.Wait = retry.Wait
	supervisor.Persist = func(recoverytypes.Status) error { return nil }
	var mu sync.Mutex
	var statuses []recoverytypes.Status
	supervisor.Emit = func(event AgentEvent) {
		mu.Lock()
		statuses = append(statuses, event.Data.(recoverytypes.Status))
		mu.Unlock()
	}
	var events []AgentEvent
	result := a.RunSupervised(
		t.Context(),
		[]provider.ChatMessage{provider.NewTextMessage("user", "finish the task")},
		"system", func(event AgentEvent) { events = append(events, event) },
		nil, "session", nil,
		SupervisedRunOptions{Supervisor: supervisor},
	)
	if len(server.Requests()) != 2 {
		t.Fatalf("requests=%d want=2", len(server.Requests()))
	}
	for _, event := range events {
		if event.Type == "error" {
			t.Fatalf("recoverable attempt leaked terminal error: %#v", event)
		}
	}
	if len(statuses) != 3 || statuses[0].State != recoverytypes.Recovering ||
		statuses[1].State != recoverytypes.Recovering || !statuses[1].RetryAt.IsZero() ||
		statuses[2].State != recoverytypes.Recovered ||
		statuses[0].ID != statuses[2].ID {
		t.Fatalf("recovery statuses = %+v", statuses)
	}
	found := 0
	for _, message := range result.Archive {
		if message.Role == "assistant" && message.ContentString() == "finished after recovery" {
			found++
		}
	}
	if found != 1 {
		t.Fatalf("canonical recovered output count=%d history=%+v", found, result.Archive)
	}
}

func TestRunSupervisedDiscardsTruncatedToolArgumentsAndContinues(t *testing.T) {

	server := providersim.New(t)
	server.Respond(func(r providersim.Request) providersim.Turn {
		if r.Index == 1 {
			return providersim.Text("Planning safely.").WithTools(providersim.ToolCall{ID: "partial", Name: "write_file", Arguments: `{"path":"danger`}).WithFinish(providersim.FinishLength)
		}
		last := simulatedText(r.Messages[len(r.Messages)-1].Parts)
		if !strings.Contains(last, `runtime_recovery kind="partial_tool_call"`) {
			t.Errorf("second request lacked canonical partial-tool recovery: %q", last)
		}
		return providersim.Text("continued without replay")
	})
	p := provider.FromPreset("openai-chat", "key", server.BaseURL(providersim.Chat))
	p.SetDialSettings(provider.DialSettings{MaxAttempts: 1})
	a := newTestAgent(t, p, "supervised-model")
	a.ContextCompactionPercent = 0
	var executions atomic.Int32
	a.ToolFn = func(context.Context, string, map[string]any) (tools.ToolResult, error) {
		executions.Add(1)
		return tools.ToolResult{}, nil
	}
	supervisor := NewSupervisor("child", 1, nil)
	supervisor.RetryBase = 0
	supervisor.Persist = func(recoverytypes.Status) error { return nil }
	result := a.RunSupervised(
		t.Context(), []provider.ChatMessage{provider.NewTextMessage("user", "work")},
		"system", func(AgentEvent) {}, nil, "child", nil,
		SupervisedRunOptions{Supervisor: supervisor},
	)
	if len(server.Requests()) != 2 || executions.Load() != 0 {
		t.Fatalf("requests=%d executions=%d", len(server.Requests()), executions.Load())
	}
	for _, message := range result.Archive {
		if len(message.ToolCalls) > 0 {
			t.Fatalf("incomplete tool call entered canonical history: %+v", message.ToolCalls)
		}
	}
}

func TestRunSupervisedWithoutOwnerDoesNotStartProvider(t *testing.T) {
	server := providersim.New(t)
	server.Script(providersim.Text("must not be used"))
	a := newTestAgent(t, provider.FromPreset("openai-chat", "key", server.BaseURL(providersim.Chat)), "model")
	var events []AgentEvent
	input := []provider.ChatMessage{provider.NewTextMessage("user", "pending")}
	result := a.RunSupervised(t.Context(), input, "system", func(event AgentEvent) {
		events = append(events, event)
	}, nil, "session", nil, SupervisedRunOptions{})
	if len(server.Requests()) != 0 || len(result.Archive) != 1 || result.State != SupervisedRunParked || len(events) != 1 || events[0].Type != "error" {
		t.Fatalf("unowned run executed: requests=%d result=%+v events=%+v", len(server.Requests()), result, events)
	}
}
