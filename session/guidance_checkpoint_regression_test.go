package session

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"crude/agent"
	"crude/collaboration"
	"crude/provider"
	"crude/sessiondb"
	"crude/tools"
	recoverytypes "crude/types/recovery"
)

// The guidance tests used to omit persistence. A deferred call was marked done
// without its result message; an allowed sibling then committed that zero value.
// The real context owner correctly refused the subsequent completed tool round,
// leaving both normal completion and recovery unable to advance.
func TestGuidanceDeferredResultsSurviveDurableCheckpoints(t *testing.T) {
	for _, child := range []bool{false, true} {
		for _, order := range []string{"write-read", "read-write", "read-write-read", "write-batch", "write", "read"} {
			t.Run(fmt.Sprintf("child=%t/%s", child, order), func(t *testing.T) {
				testGuidanceDeferredCheckpoint(t, child, order)
			})
		}
	}
}

func testGuidanceDeferredCheckpoint(t *testing.T, child bool, order string) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	const rule = "Inspect the nested source before changing its output."
	if err := os.WriteFile(filepath.Join(dir, "nested", "AGENTS.md"), []byte(rule), 0600); err != nil {
		t.Fatal(err)
	}
	var requests, reads, writes atomic.Int32
	var calls []map[string]any
	deferredIDs := map[string]bool{}
	for i, name := range strings.Split(order, "-") {
		id := fmt.Sprintf("call-%d", i)
		args := `{"path":"nested/source.txt"}`
		switch name {
		case "write":
			name, args = "write_file", `{"path":"nested/out.txt","content":"checked"}`
			deferredIDs[id] = true
		case "read":
			name = "read_file"
		case "batch":
			name, args = "multi_tool_use.parallel", `{"tool_uses":[{"recipient_name":"functions.read_file","parameters":{"path":"nested/source.txt"}}]}`
		}
		calls = append(calls, map[string]any{"index": i, "id": id, "type": "function", "function": map[string]any{"name": name, "arguments": args}})
	}
	writeCalls := func(w http.ResponseWriter, calls []map[string]any) {
		body, err := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": calls}, "finish_reason": "tool_calls"}}})
		if err != nil {
			t.Error(err)
			return
		}
		_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", body)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request provider.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		switch requests.Add(1) {
		case 1:
			writeCalls(w, calls)
		case 2:
			if writes.Load() != 0 {
				t.Error("newly governed effect ran before the model received its guidance")
			}
			foundRule := false
			results := map[string]int{}
			for _, message := range request.Messages {
				foundRule = foundRule || strings.Contains(message.ContentString(), rule)
				if message.Role == "tool" {
					results[message.ToolCallID]++
					if deferredIDs[message.ToolCallID] && !strings.Contains(message.ContentString(), "not executed") {
						t.Error("next request lost the deferred-call explanation")
					}
				}
			}
			if !foundRule {
				t.Error("next request did not receive the newly discovered rules")
			}
			for _, call := range calls {
				if results[call["id"].(string)] != 1 {
					t.Errorf("next request lacks exactly one result for %s", call["id"])
				}
			}
			if len(deferredIDs) != 0 {
				writeCalls(w, []map[string]any{{"index": 0, "id": "reassessed-write", "type": "function", "function": map[string]any{"name": "write_file", "arguments": `{"path":"nested/out.txt","content":"checked"}`}}})
				return
			}
			fallthrough
		default:
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"finished after guidance\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		}
	}))
	defer server.Close()
	home := t.TempDir()
	s, err := NewWithSessionDir("", modelAttributionConfig(server.URL, "guidance-checkpoint"), home, home)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	db, owner := s.db, s.sessionID
	if child {
		db = sessiondb.OpenAndMigrateAt(t.TempDir())
		defer db.Close()
		owner = "guidance-child"
		if err := sessiondb.CreateSessionRow(db, owner); err != nil {
			t.Fatal(err)
		}
	}
	input := canonicalContextMessage(provider.NewTextMessage("user", "inspect and update nested files"))
	input.InternalID = "guidance-assignment"
	state, err := sessiondb.PublishContext(db, sessiondb.ContextPublication{SessionID: owner, AgentID: owner,
		Mutation: sessiondb.ContextMutation{Replace: &sessiondb.ContextReplacement{Generation: 1, Reason: "test", Messages: []provider.ChatMessage{input}}}})
	if err != nil {
		t.Fatal(err)
	}
	if !child {
		s.activeContext, s.contextRevision, s.contextGeneration, s.contextReason = state.Messages, state.Revision, 1, "test"
	}
	run := newRecoveryRunState(provider.PlainInput("task"), &sessiondb.RecoveryCheckpoint{Archive: state.Messages, ActiveContext: state.Messages,
		ContextOwnerID: owner, ContextRevision: state.Revision, ContextGeneration: 1, ContextReason: "test"})
	status := recoverytypes.Status{ID: "guidance-incident", AgentID: owner, Generation: 1}
	a := agent.NewAgent(provider.FromPreset("openai-chat", "key", server.URL), "guidance-checkpoint", s.logger)
	a.WorkDir, a.Yolo = dir, true
	a.FilteredTools = append(append([]tools.ToolDef(nil), tools.AgentTools...), tools.ParallelTool())
	a.ContextArchive, a.FrozenPrefix = state.Messages, state.Messages
	a.ToolFn = func(_ context.Context, name string, _ map[string]any) (tools.ToolResult, error) {
		if name == "read_file" {
			reads.Add(1)
			return tools.ToolResult{Content: "source inspected"}, nil
		}
		writes.Add(1)
		return tools.ToolResult{Content: "output written"}, nil
	}
	accepted := map[string]provider.ChatMessage{}
	partialCheckpoints := 0
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
		checkpoint, found, err := sessiondb.LoadRecoveryCheckpoint(db, owner, owner)
		if err != nil || !found {
			return fmt.Errorf("read back checkpoint: found=%t err=%v", found, err)
		}
		for _, message := range checkpoint.ActiveContext {
			if message.Role == "" {
				t.Error("committed a blank message instead of a completed deferred-call result")
			}
			if !deferredIDs[message.ToolCallID] {
				continue
			}
			if message.InternalID == "" || !message.ToolError || !strings.Contains(message.ContentString(), "not executed") {
				t.Error("deferred call lost its stable identity, failure flag or explanation")
			}
			if earlier, ok := accepted[message.ToolCallID]; ok && !reflect.DeepEqual(earlier, message) {
				t.Error("completed round changed an already committed deferred-call result")
			}
			accepted[message.ToolCallID] = message
		}
		return nil
	}
	a.Checkpoint = func(archive, active []provider.ChatMessage) error { return publish(archive, active, nil) }
	a.CheckpointTools = func(archive, active, inFlight []provider.ChatMessage) error {
		partialCheckpoints++
		return publish(archive, active, inFlight)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	a.Run(ctx, state.Messages, "test system", nil, nil, owner, nil)
	if err := a.RunFailure(); err != nil {
		t.Fatalf("guidance-deferred batch stranded the run: %v", err)
	}
	wantRequests, wantWrites := int32(2), int32(0)
	if len(deferredIDs) != 0 {
		wantRequests, wantWrites = 3, 1
	}
	wantReads := int32(strings.Count(order, "read") + strings.Count(order, "batch"))
	if requests.Load() != wantRequests || reads.Load() != wantReads || writes.Load() != wantWrites || len(accepted) != len(deferredIDs) {
		t.Fatalf("requests=%d reads=%d writes=%d retained deferrals=%d", requests.Load(), reads.Load(), writes.Load(), len(accepted))
	}
	if wantReads != 0 && partialCheckpoints == 0 {
		t.Fatal("did not exercise a partial tool checkpoint")
	}
	checkpoint, _, err := sessiondb.LoadRecoveryCheckpoint(db, owner, owner)
	if err != nil {
		t.Fatal(err)
	}
	if got := reconcileInterruptedTools(checkpoint.ActiveContext, checkpoint.InFlightTools...); !reflect.DeepEqual(got, checkpoint.ActiveContext) {
		t.Fatal("passive recovery invented missing results in a completed guidance round")
	}
}

// Exercise normal input admission, actual file tools, terminal settlement and
// cold resume as well as the collector/publication matrix above.
func TestGuidanceDeferredRootStopAndResumeJourney(t *testing.T) {
	for _, stop := range []bool{false, true} {
		t.Run(fmt.Sprintf("stop=%t", stop), func(t *testing.T) {
			project, home, sessions := t.TempDir(), t.TempDir(), t.TempDir()
			nested := filepath.Join(project, "nested")
			if err := os.Mkdir(nested, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(nested, "AGENTS.md"), []byte("Keep the original source intact."), 0600); err != nil {
				t.Fatal(err)
			}
			source, output := filepath.Join(nested, "source.txt"), filepath.Join(nested, "out.txt")
			if err := os.WriteFile(source, []byte("original source"), 0600); err != nil {
				t.Fatal(err)
			}
			readArgs, _ := json.Marshal(map[string]string{"path": source})
			writeArgs, _ := json.Marshal(map[string]string{"path": output, "content": "only after reassessment"})
			var requests atomic.Int32
			second := make(chan struct{})
			release := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request provider.ChatRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				switch requests.Add(1) {
				case 1:
					_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"deferred-write\",\"type\":\"function\",\"function\":{\"name\":\"write_file\",\"arguments\":%q}},{\"index\":1,\"id\":\"allowed-read\",\"type\":\"function\",\"function\":{\"name\":\"read_file\",\"arguments\":%q}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n", string(writeArgs), string(readArgs))
				case 2:
					if _, err := os.Stat(output); !os.IsNotExist(err) {
						t.Error("deferred file mutation executed before reassessment")
					}
					close(second)
					if stop {
						select {
						case <-r.Context().Done():
						case <-release:
						}
						return
					}
					_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"reassessed-write\",\"type\":\"function\",\"function\":{\"name\":\"write_file\",\"arguments\":%q}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n", string(writeArgs))
				default:
					_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"normal work continues\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
				}
			}))
			defer server.Close()
			defer close(release)
			cfg := modelAttributionConfig(server.URL, "guidance-root-journey")
			s, err := NewWithSessionDir("", cfg, home, sessions)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { s.Close() }()
			s.SetWorkDir(project)
			s.SetYolo(true)
			store, err := collaboration.Attach(s.db, project, filepath.Join(home, "locks"))
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				s.Close()
				_ = store.Close()
			}()
			s.SetCollaboration(store, "guidance-root-fixture")
			sender := newMockEventSender()
			root, err := s.NewOrchestrator(sender)
			if err != nil {
				t.Fatal(err)
			}
			if !root.SubmitInput(provider.PlainInput("inspect and update nested files")) {
				t.Fatal("root input was refused")
			}
			select {
			case <-second:
			case <-time.After(5 * time.Second):
				t.Fatal("guidance deferral stranded the actual root before its next request")
			}
			if stop {
				if cancelled, _ := root.CancelTurn(); !cancelled {
					t.Fatal("Stop refused the active root")
				}
			}
			waitTransitionTurn(t, sender, root)
			before, err := sessiondb.LoadMessagesWithMeta(s.db)
			if err != nil {
				t.Fatal(err)
			}
			results := map[string]int{}
			for _, row := range before {
				if row.Msg.Role == "" {
					t.Fatal("visible transcript contains a blank checkpoint message")
				}
				if row.Msg.Role == "tool" {
					results[row.Msg.ToolCallID]++
					if row.Msg.ToolCallID == "reassessed-write" && row.Msg.ToolError {
						t.Fatalf("reassessed write failed: %s", row.Msg.ContentString())
					}
					if row.Msg.ToolCallID == "deferred-write" && (!row.Msg.ToolError || !strings.Contains(row.Msg.ContentString(), "not executed")) {
						t.Fatal("visible transcript lost the true deferral outcome")
					}
				}
			}
			if results["deferred-write"] != 1 || results["allowed-read"] != 1 {
				t.Fatal("visible transcript lost or duplicated tool outcomes")
			}
			path, count := s.dbPath, requests.Load()
			s.Close()
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			resumed, err := ResumeSessionWithDir(path, cfg, home, sessions)
			if err != nil {
				t.Fatal(err)
			}
			s = resumed
			s.SetWorkDir(project)
			store, err = collaboration.Attach(s.db, project, filepath.Join(home, "locks"))
			if err != nil {
				t.Fatal(err)
			}
			s.SetCollaboration(store, "guidance-root-resumed")
			after, err := sessiondb.LoadMessagesWithMeta(s.db)
			if err != nil || !reflect.DeepEqual(before, after) || requests.Load() != count || len(s.pendingRecoveries) != 0 {
				t.Fatalf("passive resume changed history, replayed work or retained settled recovery: %v", err)
			}
			sender = newMockEventSender()
			root, err = s.NewOrchestrator(sender)
			if err != nil {
				t.Fatal(err)
			}
			if !root.SubmitInput(provider.PlainInput("continue with a new request")) {
				t.Fatal("reopened root refused fresh input")
			}
			waitTransitionTurn(t, sender, root)
			if requests.Load() != count+1 {
				t.Fatal("new input replayed historical requests")
			}
			content, err := os.ReadFile(output)
			if stop {
				if !os.IsNotExist(err) {
					t.Fatal("Stop or passive resume executed the deferred write")
				}
			} else if err != nil || string(content) != "only after reassessment" || results["reassessed-write"] != 1 {
				t.Fatalf("reassessed mutation did not complete exactly once: read=%v content=%q results=%v", err, content, results)
			}
		})
	}
}
