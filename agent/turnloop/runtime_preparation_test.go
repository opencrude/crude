package turnloop

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"crude/agent/provider"
	"crude/internal/providersim"
)

func TestRunRuntimePreparationFailureBlocksWeakenedRequests(t *testing.T) {
	for _, boundary := range []string{"before-request", "terminal-response", "provider-error", "already-cancelled"} {
		t.Run(boundary, func(t *testing.T) {
			gate := providersim.NewGate()
			defer gate.Release()
			turn := providersim.Text("accepted original response")
			if boundary == "provider-error" {
				turn = providersim.HTTPError(providersim.InvalidRequest)
			}
			server := providersim.New(t)
			server.Script(turn.WithGate(gate))
			connection := provider.FromPreset("openai-chat", "fixture", server.BaseURL(providersim.Chat))
			connection.Model = "runtime-preparation-" + boundary
			connection.SetDialSettings(provider.DialSettings{MaxAttempts: 1})
			a := newTestAgent(t, connection, connection.Model)
			original := provider.NewTextMessage("user", "journal-authored original steering")
			original.InternalID, original.Origin = "steering-original", provider.OriginHumanSteering
			selected := provider.NewTextMessage("user", "captured selected MCP source and exact fingerprint")
			selected.InternalID = original.InternalID + "-references"
			selected.Origin, selected.Visibility = provider.OriginRuntimeContext, provider.VisibilityContextOnly
			failure := errors.New("selected media requires repair before Continue")
			var admissions, releases atomic.Int32
			inbox := make(chan RuntimeMessage, 1)
			runtime := RuntimeMessage{
				Message: original,
				Prepare: func(context.Context, []provider.ChatMessage) ([]provider.ChatMessage, error) {
					return []provider.ChatMessage{selected}, failure
				},
				Admit: func(messages []provider.ChatMessage) error {
					admissions.Add(1)
					if len(messages) < 2 || messages[len(messages)-2].Content != original.Content || messages[len(messages)-1].Content != selected.Content {
						t.Error("runtime admission did not preserve the original and captured selection")
					}
					return nil
				},
				Release: func(string) { releases.Add(1) },
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if boundary == "before-request" || boundary == "already-cancelled" {
				inbox <- runtime
			}
			if boundary == "already-cancelled" {
				cancel()
			}
			done := make(chan []provider.ChatMessage, 1)
			var toolEvents atomic.Int32
			go func() {
				done <- a.Run(ctx, nil, "system", func(event AgentEvent) {
					if strings.HasPrefix(event.Type, "tool_") {
						toolEvents.Add(1)
					}
				}, nil, "runtime-preparation", inbox)
			}()
			if boundary == "terminal-response" || boundary == "provider-error" {
				wait, stop := context.WithTimeout(t.Context(), 3*time.Second)
				defer stop()
				if err := gate.Wait(wait); err != nil {
					t.Fatalf("initial request did not start: %v", err)
				}
				inbox <- runtime
				gate.Release()
			}
			select {
			case archive := <-done:
				for _, expected := range []provider.ChatMessage{original, selected} {
					count := 0
					for _, message := range archive {
						if message.InternalID == expected.InternalID {
							count++
							if message.Content != expected.Content {
								t.Fatalf("runtime preparation changed immutable source %q", expected.InternalID)
							}
						}
					}
					if count != 1 {
						t.Fatalf("source %q retained %d times", expected.InternalID, count)
					}
				}
			case <-time.After(3 * time.Second):
				t.Fatal("runtime preparation failure did not end the unsafe attempt")
			}
			wantRequests := 0
			if boundary == "terminal-response" || boundary == "provider-error" {
				wantRequests = 1
			}
			if len(server.Requests()) != wantRequests || toolEvents.Load() != 0 || admissions.Load() != 1 || releases.Load() != 1 {
				t.Fatalf("unsafe runtime lifecycle: requests=%d tools=%d admissions=%d releases=%d", len(server.Requests()), toolEvents.Load(), admissions.Load(), releases.Load())
			}
			if boundary != "already-cancelled" && (a.RunFailureKind() != RecoveryFailureConfiguration || !errors.Is(a.RunFailure(), failure)) {
				t.Fatalf("preparation error was swallowed/reclassified: kind=%s err=%v", a.RunFailureKind(), a.RunFailure())
			}
		})
	}
}
