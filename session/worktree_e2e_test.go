package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"crude/agent"
	"crude/collaboration"
	"crude/provider"
	"crude/sessiondb"
	"crude/worktree"
)

// This adapter supplies accepted document bytes exactly as the workspace owner
// does. Integration is deliberately unavailable: this journey proves launch and
// local effects, not the independently owned apply transaction.
type launchExecutionFixture struct {
	*worktree.Service
	documents map[string]worktree.Document
	failed    bool
}

func (f *launchExecutionFixture) Create(ctx context.Context, req worktree.Request) (worktree.Context, error) {
	if f.failed {
		return worktree.Context{}, errors.New("fixture setup refused")
	}
	req.Documents = f.documents
	return f.Service.Create(ctx, req)
}
func (f *launchExecutionFixture) Apply(context.Context, worktree.PlanDelta) (worktree.Receipt, error) {
	return worktree.Receipt{}, errors.New("integration not owned by launch fixture")
}

func TestWorktreeRealChildLaunchesPreserveImmutableAssignmentAndIsolateEffects(t *testing.T) {
	root := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	git("init", "-q")
	for path, text := range map[string]string{"dirty.txt": "committed\n", "accepted.txt": "disk\n", "AGENTS.md": "Use the isolated fixture root.\n"} {
		if err := os.WriteFile(filepath.Join(root, path), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	git("add", ".")
	git("-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "baseline")
	if err := os.WriteFile(filepath.Join(root, "dirty.txt"), []byte("dirty baseline\n"), 0600); err != nil {
		t.Fatal(err)
	}
	storePath := filepath.Join(t.TempDir(), "executions")
	service, err := worktree.Open(root, storePath, worktree.Options{})
	if err != nil {
		t.Fatal(err)
	}
	fixture := &launchExecutionFixture{Service: service, documents: map[string]worktree.Document{"accepted.txt": {Revision: 7, Exists: true, Data: []byte("accepted document\n"), Mode: 0600}}}
	var mu sync.Mutex
	steps := map[string]int{}
	requests := map[string][]string{}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			Messages []provider.ChatMessage `json:"messages"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		encoded, _ := json.Marshal(body)
		key := "first"
		if strings.Contains(string(encoded), "SECOND_CHILD") {
			key = "second"
		}
		mu.Lock()
		step := steps[key]
		steps[key]++
		requests[key] = append(requests[key], string(encoded))
		mu.Unlock()
		calls.Add(1)
		var name string
		var params map[string]any
		switch step {
		case 0:
			name, params = "read_file", map[string]any{"path": "dirty.txt"}
		case 1:
			name, params = "read_file", map[string]any{"path": "accepted.txt"}
		case 2:
			name, params = "write_file", map[string]any{"path": "overlap.txt", "content": key + "\n"}
		case 3:
			name, params = "read_file", map[string]any{"path": "overlap.txt"}
		case 4:
			name, params = "edit_file", map[string]any{"path": "overlap.txt", "old_string": key + "\n", "new_string": key + " edited\n"}
		case 5:
			name, params = "apply_patch", map[string]any{"input": "*** Begin Patch\n*** Update File: overlap.txt\n@@\n-" + key + " edited\n+" + key + " patched\n*** End Patch"}
		case 6:
			name, params = "execute_command", map[string]any{"command": "pwd; cat dirty.txt accepted.txt overlap.txt"}
		case 7:
			name, params = "exec_command", map[string]any{"cmd": "pwd; cat overlap.txt", "yield_time_ms": 1000}
		case 8, 9:
			// A parent-supplied absolute path must not redirect a managed child
			// into the logical checkout, even though absolute reads are allowed.
			// First intake the logical checkout's guidance, then retry the effect.
			name, params = "write_file", map[string]any{"path": filepath.Join(root, "escaped.txt"), "content": "must not escape\n"}
		case 10:
			name, params = "read_file", map[string]any{"path": "overlap.txt"}
		case 11:
			// Real child-authored report required by the runtime, addressed to its
			// exact parent. These fixtures use the session root collaborator ID.
			name, params = "publish_report", map[string]any{"kind": "final", "recipient_ids": []string{"worktree-parent"}, "summary": key + " isolated proof", "body": "Isolated effects verified."}
		default:
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Finished isolated proof.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			return
		}
		if key == "first" {
			if step == 5 {
				name, params = "edit_file", map[string]any{"path": "overlap.txt", "old_string": "first edited\n", "new_string": "first patched\n"}
			}
			if step == 7 {
				name, params = "execute_command", map[string]any{"command": "pwd; cat overlap.txt"}
			}
		} else {
			switch step {
			case 2:
				name, params = "apply_patch", map[string]any{"input": "*** Begin Patch\n*** Add File: overlap.txt\n+second\n*** End Patch"}
			case 4:
				name, params = "apply_patch", map[string]any{"input": "*** Begin Patch\n*** Update File: overlap.txt\n@@\n-second\n+second edited\n*** End Patch"}
			case 8, 9:
				name, params = "apply_patch", map[string]any{"input": "*** Begin Patch\n*** Add File: " + filepath.Join(root, "escaped.txt") + "\n+must not escape\n*** End Patch"}
			}
		}
		args, _ := json.Marshal(params)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":%q,\"type\":\"function\",\"function\":{\"name\":%q,\"arguments\":%q}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n", fmt.Sprintf("%s-%d", key, step), name, string(args))
	}))
	defer server.Close()
	cfg := modelAttributionConfig(server.URL, "worktree-model")
	profile := cfg.Agents.Profiles[cfg.Agents.ActiveProfile]
	profile.Categories[CategoryCrafter] = CategoryConfig{AccountID: "account", Model: "worktree-model"}
	cfg.Agents.Profiles[cfg.Agents.ActiveProfile] = profile
	cfg.Agents.AgentProfiles = map[string]AgentProfileConfig{}
	cfg.Agents.AgentProfiles["native"] = AgentProfileConfig{Category: CategoryCrafter, AccountID: "account", Model: "gpt-5.3-codex"}
	home := t.TempDir()
	directory := filepath.Join(home, "sessions")
	s, err := NewWithSessionDir("worktree-parent", cfg, home, directory)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.SetWorkDir(root)
	s.SetExecutionService(fixture)
	s.SetYolo(true)
	store, err := collaboration.Attach(s.db, root, filepath.Join(home, "locks"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	s.SetCollaboration(store, "worktree-owner")
	r := NewSubAgentRunner(s, 2)
	s.subAgentSup = r
	r.SetOrchestratorID("worktree-parent")
	// Register the exact root recipient without invoking the root provider.
	if _, err := s.registerCollaborator(t.Context(), "worktree-parent", "Parent", s.sessionID, "", CategoryDefault, s.db); err != nil {
		t.Fatal(err)
	}
	var handles []*agent.SubAgentHandle
	for _, marker := range []string{"FIRST_CHILD", "SECOND_CHILD"} {
		params := map[string]any{"category": CategoryCrafter, "description": marker, "instructions": "Perform the fixture sequence", "file_ownership": []string{"overlap.txt"}}
		if marker == "SECOND_CHILD" {
			params["profile"] = "native"
			delete(params, "file_ownership") // Exercise checkout isolation without a scope.
		}
		result, err := r.StartAgent(t.Context(), params)
		if err != nil {
			t.Fatalf("real StartAgent: %v: %s", err, result.Content)
		}
		var identity struct {
			AgentID string `json:"agent_id"`
		}
		if err := json.Unmarshal([]byte(result.Content), &identity); err != nil {
			t.Fatal(err)
		}
		handle, ok := r.mgr.GetActive(agent.AgentID(identity.AgentID))
		if !ok {
			t.Fatal("child missing from manager")
		}
		handles = append(handles, handle)
	}
	done := make(chan struct{})
	go func() { r.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		r.CancelAllActive()
		t.Fatal("real child tool loop did not finish")
	}
	if handles[0].Spec.ExecutionRoot == handles[1].Spec.ExecutionRoot {
		t.Fatal("children shared execution geometry")
	}
	for i, h := range handles {
		key := []string{"first", "second"}[i]
		data, err := os.ReadFile(filepath.Join(h.Spec.ExecutionRoot, "overlap.txt"))
		if err != nil || string(data) != key+" patched\n" {
			mu.Lock()
			wire := strings.Join(requests[key], "\n")
			mu.Unlock()
			t.Fatalf("child effects=%q err=%v wire=%s", data, err, wire)
		}
		retained, err := fixture.Inspect(h.Spec.ExecutionID)
		if err != nil || len(retained.Pins) != 0 || retained.Removed {
			t.Fatalf("settled artifact %+v err=%v", retained, err)
		}
		if !h.ReportDone() {
			t.Fatal("real final report missing")
		}
		db, err := sessiondb.OpenAndMigrate(s.completedSubAgentDBPath(string(h.Spec.ID)))
		if err != nil {
			t.Fatal(err)
		}
		state, found, err := sessiondb.LoadContextState(db, h.SessionID, string(h.Spec.ID))
		_ = db.Close()
		if err != nil || !found || len(state.Messages) == 0 {
			t.Fatalf("immutable DB context found=%v err=%v", found, err)
		}
		if state.Messages[0].InternalID != "assignment-"+string(h.Spec.ID) {
			t.Fatalf("assignment identity changed: %+v", state.Messages[0])
		}
		for _, message := range state.Messages {
			if message.Role != "tool" {
				continue
			}
			if message.ToolCallID == key+"-8" {
				if !strings.Contains(message.ContentString(), "This call was not executed") {
					t.Fatalf("expected guidance intake before escape retry: %s", message.ContentString())
				}
			} else if message.ToolCallID == key+"-9" {
				if !strings.Contains(message.ContentString(), "outside the execution checkout") {
					t.Fatalf("absolute escape was not rejected: %s", message.ContentString())
				}
			} else if message.ToolError || agent.IsToolError(message.ContentString()) {
				t.Fatalf("unexpected failure for %s: %s", message.ToolCallID, message.ContentString())
			}
		}
		mu.Lock()
		wire := strings.Join(requests[key], "\n")
		count := len(requests[key])
		mu.Unlock()
		// The wire is JSON, so the root's separators appear escaped on Windows.
		encodedRoot, _ := json.Marshal(h.Spec.ExecutionRoot)
		if count != 13 || !strings.Contains(wire, strings.Trim(string(encodedRoot), `"`)) || !strings.Contains(wire, "dirty baseline") || !strings.Contains(wire, "accepted document") || !strings.Contains(wire, key+" patched") || !strings.Contains(wire, "Use the isolated fixture root.") || !strings.Contains(wire, "outside the execution checkout") {
			for _, message := range state.Messages {
				if message.Role == "tool" && (message.ToolCallID == key+"-8" || message.ToolCallID == key+"-9") {
					t.Logf("escape attempt: %s", message.ContentString())
				}
			}
			t.Fatalf("real wire proof failed count=%d checkout=%q boundary rejection=%v", count, h.Spec.ExecutionRoot, strings.Contains(wire, "outside the execution checkout"))
		}
	}
	if _, err := os.Stat(filepath.Join(root, "overlap.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("logical source was mutated")
	}
	if _, err := os.Stat(filepath.Join(root, "escaped.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("absolute path escaped into logical source")
	}
	if data, _ := os.ReadFile(filepath.Join(root, "dirty.txt")); string(data) != "dirty baseline\n" {
		t.Fatal("dirty source changed")
	}
	if data, _ := os.ReadFile(filepath.Join(root, "accepted.txt")); string(data) != "disk\n" {
		t.Fatal("accepted document was flushed into logical disk")
	}
	before := calls.Load()
	fixture.failed = true
	if _, err := r.StartAgent(t.Context(), map[string]any{"description": "FAILED_SETUP", "isolation": "worktree"}); err == nil {
		t.Fatal("failed setup was accepted")
	}
	if calls.Load() != before {
		t.Fatal("failed setup invoked provider")
	}
	// Opening retained artifacts is passive and does not recreate or unpin work.
	reopened, err := worktree.Open(root, storePath, worktree.Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range handles {
		retained, err := reopened.Inspect(h.Spec.ExecutionID)
		if err != nil || retained.Root != h.Spec.ExecutionRoot || !retained.Ready {
			t.Fatalf("passive retained root %+v err=%v", retained, err)
		}
		_, rows, _, err := loadSubAgentSnapshotDB(s.completedSubAgentDBPath(string(h.Spec.ID)), string(h.Spec.ID))
		if err != nil || len(rows) == 0 {
			t.Fatalf("passive child read rows=%d err=%v", len(rows), err)
		}
	}
	if calls.Load() != before {
		t.Fatal("passive reopen replayed a request")
	}
}
