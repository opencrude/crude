package agent

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"crude/provider"
	"crude/retry"
	recoverytypes "crude/types/recovery"
)

func TestHTTPGatewayRecoveryContinuesBeyondMaximumCooldownForEveryProtocol(t *testing.T) {
	fixtures := supervisedProtocolFixtures()
	for _, protocol := range provider.ProtocolList() {
		id := protocol.Info().ID
		fixture, ok := fixtures[id]
		if !ok {
			t.Fatalf("missing fixture for protocol %s", id)
		}
		for _, child := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/child=%t", id, child), func(t *testing.T) {
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					if requests.Add(1) <= 3 {
						w.Header().Set("Retry-After", "86400")
						w.WriteHeader(http.StatusRequestTimeout)
						_, _ = fmt.Fprint(w, `{"error":{"type":"invalid_request_error","message":"stream error: stream disconnected before completion: stream closed before response.completed"}}`)
						return
					}
					w.Header().Set("Content-Type", fixture.contentType)
					_, _ = fmt.Fprint(w, fixture.complete)
				}))
				defer server.Close()
				p := provider.FromPreset("openai-chat", "key", server.URL)
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
				if result.State != SupervisedRunCompleted || requests.Load() != 4 || len(waits) != 3 || len(countdowns) != 3 {
					t.Fatalf("state=%s requests=%d waits=%v countdowns=%+v cause=%v", result.State, requests.Load(), waits, countdowns, result.Failure.Cause)
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
