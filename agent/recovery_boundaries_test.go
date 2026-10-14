package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"crude/provider"
	"crude/tools"
	recoverytypes "crude/types/recovery"
)

// The agentic loop is sacred: a tool panic, alone or inside a native parallel
// batch, reaches the model as a tool error and the supervised run completes
// without opening a recovery incident.
func TestToolPanicContinuesSupervisedRunWithoutIncident(t *testing.T) {
	for _, parallel := range []bool{false, true} {
		t.Run(fmt.Sprintf("parallel=%t", parallel), func(t *testing.T) {
			var requests atomic.Int32
			sawError := make(chan string, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if requests.Add(1) > 1 {
					// The raw request carries the tool error wherever the wire
					// placed it (a tool message or batch content parts).
					body, _ := io.ReadAll(r.Body)
					select {
					case sawError <- string(body):
					default:
					}
					writeChatSSE(w, "inspected and finished", "stop")
					return
				}
				name, args := "read_file", `{"path":"uncertain"}`
				if parallel {
					name = "multi_tool_use.parallel"
					args = `{"tool_uses":[{"recipient_name":"functions.read_file","parameters":{"path":"uncertain"}}]}`
				}
				encoded, _ := json.Marshal(args)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-1\",\"type\":\"function\",\"function\":{\"name\":%q,\"arguments\":%s}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n", name, encoded)
			}))
			defer server.Close()
			a := newTestAgent(t, provider.FromPreset("openai-chat", "key", server.URL), "panic-boundary")
			a.FilteredTools = append(append([]tools.ToolDef(nil), tools.AgentTools...), tools.ParallelTool())
			a.ToolFn = func(context.Context, string, map[string]any) (tools.ToolResult, error) {
				panic("effect may already have happened")
			}
			statuses := make(chan recoverytypes.Status, 8)
			supervisor := NewSupervisor("root", 1, nil)
			supervisor.Persist = func(recoverytypes.Status) error { return nil }
			supervisor.Emit = func(event AgentEvent) { statuses <- event.Data.(recoverytypes.Status) }
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			result := a.RunSupervised(ctx, []provider.ChatMessage{provider.NewTextMessage("user", "task")},
				"system", nil, nil, "session", nil, SupervisedRunOptions{Supervisor: supervisor})
			if result.State != SupervisedRunCompleted || requests.Load() != 2 {
				t.Fatalf("tool panic ended the run: state=%s requests=%d", result.State, requests.Load())
			}
			select {
			case status := <-statuses:
				t.Fatalf("tool panic opened a recovery incident: %+v", status)
			default:
			}
			if text := <-sawError; !strings.Contains(text, "panicked") || !strings.Contains(text, "effects are uncertain") {
				t.Fatalf("model did not receive the uncertain-effect tool error: %q", text)
			}
		})
	}
}

// Hannah's incident: a lost HTTP/2 connection after partial output surfaced as
// an unrecognized sentinel and was parked as a settings fault. Any failure
// without positive evidence of permanence recovers automatically.
func TestUnknownTransportFailureRecoversAutomatically(t *testing.T) {
	lost := errors.New("http2: client connection lost")
	for _, err := range []error{
		&apiCallError{Message: "stream interrupted after partial response: " + lost.Error(), Partial: true, Cause: lost},
		&apiCallError{Message: "stream interrupted", Partial: true, Cause: &provider.StreamInterruptionError{Kind: provider.StreamInterruptionTransport, Cause: lost}},
		&provider.ExhaustedError{Attempts: 3, Cause: lost},
		errors.New("proxy: upstream went away"),
	} {
		failure := classifyRecordedRecovery("", err)
		if failure.Kind != RecoveryFailureTransport || failure.AutomaticLimit >= 0 {
			t.Fatalf("%v: recovery = %+v, want unbounded automatic transport recovery", err, failure)
		}
	}
	verdict := classifyRecordedRecovery("", &provider.APIError{Message: "unrecognized backend failure"})
	if verdict.AutomaticLimit <= 0 {
		t.Fatalf("unrecognized provider verdict was not retried automatically: %+v", verdict)
	}
	for _, err := range []error{&provider.StatusError{StatusCode: 401}, &provider.APIError{Code: "insufficient_quota"}} {
		if failure := classifyRecordedRecovery("", err); failure.AutomaticLimit != 0 || failure.Kind != RecoveryFailureConfiguration {
			t.Fatalf("%v: permanent evidence retried automatically: %+v", err, failure)
		}
	}
	if failure := classifyRecordedRecovery(RecoveryFailureCompaction, errors.New("compacted context remains above threshold")); failure.AutomaticLimit != 0 {
		t.Fatalf("local compaction fault would repeat the pipeline: %+v", failure)
	}
	if failure := classifyRecordedRecovery(RecoveryFailureCompaction, &provider.ExhaustedError{Attempts: 3, Cause: lost}); failure.AutomaticLimit >= 0 {
		t.Fatalf("compaction provider outage did not recover automatically: %+v", failure)
	}
}

func TestBoundedRecoveryExhaustionSaysAutomaticRecoveryStopped(t *testing.T) {
	supervisor := NewSupervisor("owner", 1, nil)
	supervisor.Wait = func(context.Context, time.Duration) error { return nil }
	attention := make(chan recoverytypes.Status, 1)
	supervisor.Attention = func(status recoverytypes.Status) { attention <- status }
	failure := VerdictRecovery(&provider.APIError{Message: "unrecognized backend failure"})
	for attempt := 1; attempt <= failure.AutomaticLimit; attempt++ {
		if !supervisor.Await(t.Context(), failure) {
			t.Fatalf("automatic attempt %d did not continue", attempt)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan bool, 1)
	go func() { done <- supervisor.Await(ctx, failure) }()
	status := <-attention
	cancel()
	<-done
	if status.State != recoverytypes.NeedsAttention || !strings.Contains(status.Action, "Automatic recovery stopped after 5 attempts") {
		t.Fatalf("exhausted bounded recovery = %+v", status)
	}
}

func TestSupervisorRetryNowShortensCooldownWithoutWaitingForTimer(t *testing.T) {
	wake := make(chan struct{}, 1)
	waiting := make(chan struct{})
	waitCancelled := make(chan struct{})
	supervisor := NewSupervisor("owner", 7, nil)
	supervisor.Wake = wake
	supervisor.Wait = func(ctx context.Context, _ time.Duration) error {
		close(waiting)
		<-ctx.Done()
		close(waitCancelled)
		return ctx.Err()
	}
	done := make(chan bool, 1)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	go func() {
		done <- supervisor.Await(ctx, TransientRecovery(&provider.StatusError{
			StatusCode: 503, RetryAfter: 5 * time.Minute,
		}))
	}()
	<-waiting
	wake <- struct{}{}
	select {
	case continued := <-done:
		if !continued {
			t.Fatal("Retry now was treated as cancellation")
		}
	case <-ctx.Done():
		t.Fatal("Retry now did not shorten the five-minute cooldown")
	}
	<-waitCancelled
	status, _ := supervisor.Current()
	if !status.RetryAt.IsZero() || status.State != recoverytypes.Recovering {
		t.Fatalf("Retry now retained a stale countdown: %+v", status)
	}
}

func TestTaskSupervisorAutomaticallyRechecksDNSButNotInvalidCredentials(t *testing.T) {
	dns := classifyRecoveryError(&net.DNSError{Err: "no such host", Name: "provider.invalid", IsNotFound: true})
	if dns.AutomaticLimit >= 0 || dns.Kind != RecoveryFailureTransport {
		t.Fatalf("DNS recovery requires manual retry: %+v", dns)
	}
	credentials := classifyRecoveryError(&provider.StatusError{StatusCode: 401, Message: "rejected"})
	if credentials.AutomaticLimit != 0 || credentials.Kind != RecoveryFailureConfiguration {
		t.Fatalf("credential failure loops automatically: %+v", credentials)
	}
}

func TestRunEarlyFailurePreservesArchiveAndActiveContext(t *testing.T) {
	a := newTestAgent(t, provider.FromPreset("openai-chat", "key", ""), "early-failure")
	a.Provider = nil
	archive := []provider.ChatMessage{
		provider.NewTextMessage("user", "old task"),
		provider.NewTextMessage("assistant", "old accepted work"),
	}
	active := []provider.ChatMessage{provider.NewTextMessage("user", "compacted task")}
	a.ContextArchive = archive
	returned := a.Run(t.Context(), active, "system", nil, nil, "session", nil)
	if len(returned) != 2 || returned[1].ContentString() != "old accepted work" {
		t.Fatalf("early failure replaced full archive with compacted context: %+v", returned)
	}
	if got := a.ActiveContext(); len(got) != 1 || got[0].ContentString() != "compacted task" {
		t.Fatalf("early failure lost active context: %+v", got)
	}
}

func TestCompletedParallelSiblingIsCheckpointedWhileOtherToolRuns(t *testing.T) {
	for _, batch := range []bool{false, true} {
		t.Run(fmt.Sprintf("native_batch=%t", batch), func(t *testing.T) { checkCompletedParallelSibling(t, batch) })
	}
}

func checkCompletedParallelSibling(t *testing.T, batch bool) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request provider.ChatRequest
		_ = json.NewDecoder(r.Body).Decode(&request)
		if request.Messages[len(request.Messages)-1].Role == "tool" {
			writeChatSSE(w, "done", "stop")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if batch {
			arguments, _ := json.Marshal(`{"tool_uses":[{"recipient_name":"functions.read_file","parameters":{"path":"slow"}},{"recipient_name":"functions.read_file","parameters":{"path":"fast"}}]}`)
			_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"batch\",\"type\":\"function\",\"function\":{\"name\":\"multi_tool_use.parallel\",\"arguments\":%s}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n", arguments)
		} else {
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"slow\",\"type\":\"function\",\"function\":{\"name\":\"read_file\",\"arguments\":\"{\\\"path\\\":\\\"slow\\\"}\"}},{\"index\":1,\"id\":\"fast\",\"type\":\"function\",\"function\":{\"name\":\"read_file\",\"arguments\":\"{\\\"path\\\":\\\"fast\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
		}
	}))
	defer server.Close()
	a := newTestAgent(t, provider.FromPreset("openai-chat", "key", server.URL), "checkpoint-boundary")
	a.FilteredTools = append(append([]tools.ToolDef(nil), tools.AgentTools...), tools.ParallelTool())
	release := make(chan struct{})
	a.ToolFn = func(ctx context.Context, _ string, params map[string]any) (tools.ToolResult, error) {
		if params["path"] == "slow" {
			select {
			case <-release:
			case <-ctx.Done():
			}
		}
		return tools.ToolResult{Content: "accepted " + params["path"].(string)}, nil
	}
	checkpointed := make(chan struct{}, 1)
	a.Checkpoint = func(archive, active []provider.ChatMessage) error {
		for _, message := range active {
			if message.Role == "tool" && strings.Contains(message.ContentString(), "accepted fast") {
				select {
				case checkpointed <- struct{}{}:
				default:
				}
			}
		}
		return nil
	}
	a.CheckpointTools = func(archive, active, inFlight []provider.ChatMessage) error {
		return a.Checkpoint(archive, append(append([]provider.ChatMessage(nil), active...), inFlight...))
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		a.Run(ctx, nil, "system", nil, nil, "session", nil)
		close(done)
	}()
	select {
	case <-checkpointed:
	case <-time.After(time.Second):
		t.Error("finished sibling had no durable boundary while another tool was running")
	}
	close(release)
	<-done
}
