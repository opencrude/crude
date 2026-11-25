package turnloop

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"crude/agent/provider"
	"crude/internal/providersim"
	"crude/internal/retry"
	recoverytypes "crude/types/recovery"
)

func TestHTTPGatewayRecoveryContinuesBeyondMaximumCooldownForEveryProtocol(t *testing.T) {
	fixtures := supervisedWires
	for _, protocol := range provider.ProtocolList() {
		id := protocol.Info().ID
		fixture, ok := fixtures[id]
		if !ok {
			t.Fatalf("missing fixture for protocol %s", id)
		}
		for _, child := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/child=%t", id, child), func(t *testing.T) {
				server := providersim.New(t)
				timeout := providersim.HTTPError(providersim.RequestTimeout).WithRetryHint(providersim.RetryAfterSeconds(86400))
				server.Script(timeout, timeout, timeout, providersim.Text("finished"))
				// This gateway reports a timeout as invalid input on every downstream wire.
				var translations atomic.Int32
				base := translatingGateway(t, server, fixture.wire, func(resp *http.Response) error {
					if resp.StatusCode == http.StatusRequestTimeout {
						translations.Add(1)
						if err := resp.Body.Close(); err != nil {
							return err
						}
						resp.Header.Set("Content-Type", "application/json")
						gatewayBody(resp, `{"error":{"type":"invalid_request_error","message":"stream error: stream disconnected before completion: stream closed before response.completed"}}`)
					}
					return nil
				})
				p := provider.FromPreset("openai-chat", "key", base)
				if err := p.SetProtocolID(id); err != nil {
					t.Fatal(err)
				}
				p.SetDialSettings(provider.DialSettings{MaxAttempts: 1})
				a := newTestAgent(t, p, "http-recovery-"+id)
				a.ContextCompactionPercent = 0
				a.AgentID = "owner"
				if child {
					a.Depth, a.ParentID, a.reportPublished = 1, "parent", true
				}
				now := time.Unix(1700000000, 0)
				supervisor := NewSupervisor("owner", 7, &recoverytypes.Status{ID: "ongoing-incident", Attempt: 9})
				supervisor.Now = func() time.Time { return now }
				var waits []time.Duration
				supervisor.Wait = func(_ context.Context, delay time.Duration) error {
					waits = append(waits, delay)
					// Three errors precede the scripted success. If the stream
					// breaks again, report it instead of spinning on an empty
					// simulator with a zero-time clock until the suite times out.
					if len(waits) > 3 {
						return fmt.Errorf("unexpected recovery after the scripted success")
					}
					return nil
				}
				var countdowns []recoverytypes.Status
				supervisor.Emit = func(event AgentEvent) {
					if status := event.Data.(recoverytypes.Status); !status.RetryAt.IsZero() {
						countdowns = append(countdowns, status)
					}
				}
				// An incorrect attention classification returns immediately instead
				// of hanging the test or needing a human wake to make it pass.
				result := a.RunSupervised(t.Context(), []provider.ChatMessage{provider.NewTextMessage("user", "retained task")}, "system", nil, nil, "session", nil,
					SupervisedRunOptions{Supervisor: supervisor, OnAttention: func(RecoveryFailure) AttentionDisposition { return AttentionParkAttempt }})
				if got := translations.Load(); got != 3 {
					t.Fatalf("gateway translated %d failures; want 3", got)
				}
				if result.State != SupervisedRunCompleted || len(server.Requests()) != 4 || len(waits) != 3 || len(countdowns) != 3 {
					t.Fatalf("state=%s requests=%d waits=%v countdowns=%+v cause=%v", result.State, len(server.Requests()), waits, countdowns, result.Failure.Cause)
				}
				for i, status := range countdowns {
					if waits[i] != retry.MaxCooldown || status.RetryAt.Sub(now) != 5*time.Minute || status.State != recoverytypes.Recovering || status.ID != "ongoing-incident" || status.AgentID != "owner" || status.Generation != 7 {
						t.Fatalf("retry %d lost deadline/ownership: wait=%s status=%+v", i, waits[i], status)
					}
				}
			})
		}
	}
}
