package session

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"crude/agent"
	"crude/provider"
	"crude/sessiondb"
	"crude/tools"
	recoverytypes "crude/types/recovery"
)

// Exercise the real provider -> tool collector -> session publication transaction,
// not an always-successful checkpoint callback. The old tests missed this boundary.
func TestParallelCheckpointsRetainImmutableContextAcrossCompletion(t *testing.T) {
	for _, native := range []bool{false, true} {
		for _, child := range []bool{false, true} {
			t.Run(fmt.Sprintf("native=%t/child=%t", native, child), func(t *testing.T) {
				var requests, effects atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "text/event-stream")
					if requests.Add(1) > 1 {
						_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"finished\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
						return
					}
					var calls []map[string]any
					if native {
						args := `{"tool_uses":[{"recipient_name":"functions.read_file","parameters":{"path":"slow"}},{"recipient_name":"functions.read_file","parameters":{"path":"fast"}}]}`
						calls = append(calls, map[string]any{"index": 0, "id": "batch", "type": "function", "function": map[string]any{"name": "multi_tool_use.parallel", "arguments": args}})
					} else {
						for i, path := range []string{"slow", "fast"} {
							calls = append(calls, map[string]any{"index": i, "id": path, "type": "function", "function": map[string]any{"name": "read_file", "arguments": fmt.Sprintf(`{"path":%q}`, path)}})
						}
					}
					body, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": calls}, "finish_reason": "tool_calls"}}})
					_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", body)
				}))
				defer server.Close()
				home := t.TempDir()
				s, err := NewWithSessionDir("", modelAttributionConfig(server.URL, "checkpoint-test"), home, home)
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				db := sessiondb.OpenAndMigrateAt(t.TempDir())
				defer db.Close()
				owner := "child"
				if !child {
					db = s.db
					owner = s.sessionID
				}
				if child {
					if err := sessiondb.CreateSessionRow(db, owner); err != nil {
						t.Fatal(err)
					}
				}
				input := provider.NewTextMessage("user", "retain all accepted results")
				input.InternalID = "assignment"
				input = canonicalContextMessage(input)
				state, err := sessiondb.PublishContext(db, sessiondb.ContextPublication{SessionID: owner, AgentID: owner, Mutation: sessiondb.ContextMutation{Replace: &sessiondb.ContextReplacement{Generation: 1, Reason: "test", Messages: []provider.ChatMessage{input}}}})
				if err != nil {
					t.Fatal(err)
				}
				if !child {
					s.activeContext, s.contextRevision, s.contextGeneration, s.contextReason = state.Messages, state.Revision, 1, "test"
				}
				run := newRecoveryRunState(provider.PlainInput("task"), &sessiondb.RecoveryCheckpoint{Archive: state.Messages, ActiveContext: state.Messages, ContextOwnerID: owner, ContextRevision: state.Revision, ContextGeneration: 1, ContextReason: "test"})
				status := recoverytypes.Status{ID: "incident", AgentID: owner, Generation: 1}
				a := agent.NewAgent(provider.FromPreset("openai-chat", "key", server.URL), "checkpoint-test", s.logger)
				a.FilteredTools = append(append([]tools.ToolDef(nil), tools.AgentTools...), tools.ParallelTool())
				a.ContextArchive, a.FrozenPrefix = state.Messages, state.Messages
				fastSaved := make(chan struct{}, 1)
				release := make(chan struct{})
				a.ToolFn = func(ctx context.Context, _ string, params map[string]any) (tools.ToolResult, error) {
					effects.Add(1)
					if params["path"] == "slow" {
						select {
						case <-release:
						case <-ctx.Done():
							return tools.ToolResult{}, ctx.Err()
						}
					}
					return tools.ToolResult{Content: "accepted " + params["path"].(string)}, nil
				}
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
						if msg.Role == "tool" && strings.Contains(msg.ContentString(), "accepted fast") {
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
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				done := make(chan []provider.ChatMessage, 1)
				go func() { done <- a.Run(ctx, state.Messages, "test system", nil, nil, owner, nil) }()
				select {
				case <-fastSaved:
				case <-ctx.Done():
					close(release)
					<-done
					t.Fatalf("completed fast sibling was not durably checkpointed; run failure: %v", a.RunFailure())
				}
				checkpoint, found, err := sessiondb.LoadRecoveryCheckpoint(db, owner, owner)
				if err != nil || !found {
					close(release)
					<-done
					t.Fatalf("checkpoint not loadable: %v", err)
				}
				if native && (len(checkpoint.InFlightTools) != 1 || !strings.Contains(checkpoint.InFlightTools[0].ContentString(), "accepted fast")) {
					t.Error("in-flight wrapper evidence was lost")
				}
				if native && !child {
					// Restore into a detached copy while the original slow tool remains
					// running: passive recovery may inspect, never replay that effect.
					copyDB := sessiondb.OpenAndMigrateAt(t.TempDir())
					defer copyDB.Close()
					if err := sessiondb.CreateSessionRow(copyDB, owner); err != nil {
						t.Fatal(err)
					}
					copyState, err := sessiondb.PublishContext(copyDB, sessiondb.ContextPublication{SessionID: owner, AgentID: owner, RuntimeAgentID: owner,
						Mutation: sessiondb.ContextMutation{Replace: &sessiondb.ContextReplacement{Generation: 1, Reason: "test", Messages: checkpoint.ActiveContext}}, Recovery: &checkpoint})
					if err != nil {
						t.Fatal(err)
					}
					checkpoint.ContextRevision = copyState.Revision
					restored := &Session{db: copyDB, sessionID: owner, contextRevision: copyState.Revision, contextGeneration: 1, contextReason: "test", activeContext: copyState.Messages}
					checkpoint.Archive = reconcileInterruptedTools(checkpoint.Archive, checkpoint.InFlightTools...)
					checkpoint.ActiveContext = reconcileInterruptedTools(checkpoint.ActiveContext, checkpoint.InFlightTools...)
					if err := restored.restoreRecoveryTranscript(&checkpoint); err != nil {
						t.Fatalf("passive in-flight recovery: %v", err)
					}
					if effects.Load() != 2 || requests.Load() != 1 {
						t.Fatal("passive recovery replayed execution")
					}
				}
				close(release)
				var messages []provider.ChatMessage
				select {
				case messages = <-done:
				case <-ctx.Done():
					t.Fatal("tool completion did not return")
				}
				if err := a.RunFailure(); err != nil {
					t.Fatalf("valid tool completion entered recovery: %v", err)
				}
				if requests.Load() != 2 || effects.Load() != 2 {
					t.Fatalf("requests=%d effects=%d", requests.Load(), effects.Load())
				}
				current, _, err := sessiondb.LoadContextState(db, owner, owner)
				if err != nil {
					t.Fatal(err)
				}
				if !retainsContextSource(canonicalContextMessages(recoveryArchiveMessages(messages)), current.Messages) {
					t.Fatal("final context changed committed result identities")
				}
				checkpoint, _, err = sessiondb.LoadRecoveryCheckpoint(db, owner, owner)
				if err != nil || len(checkpoint.InFlightTools) != 0 {
					t.Fatalf("completed wrapper retained stale in-flight state: %v", err)
				}
			})
		}
	}
}

func TestInterruptedBatchEvidenceRestoresWithoutReplacingCompletedResult(t *testing.T) {
	call := provider.ChatMessage{InternalID: "call-message", Role: "assistant", ToolCalls: []provider.ToolCall{{ID: "batch", Type: "function", Function: provider.FunctionCall{Name: "multi_tool_use.parallel"}}}}
	partial := provider.ChatMessage{InternalID: "partial", Role: "tool", ToolCallID: "batch", ToolError: true, Content: provider.TextContent(`{"outcome_unknown":true,"results":[{"result":"completed leaf"},null]}`)}
	got := reconcileInterruptedTools([]provider.ChatMessage{call}, partial)
	if len(got) != 2 || !reflect.DeepEqual(got[1], partial) {
		t.Fatal("lost completed batch leaf evidence")
	}
	complete := partial
	complete.InternalID = "complete"
	complete.ToolError = false
	complete.Content = provider.TextContent("all finished")
	got = reconcileInterruptedTools([]provider.ChatMessage{call, complete}, partial)
	if len(got) != 2 || !reflect.DeepEqual(got[1], complete) {
		t.Fatal("stale partial overrode completed result")
	}
}
