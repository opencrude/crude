package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"

	"crude/collaboration"
	"crude/provider"
	"crude/sessiondb"
	"crude/worktree"
)

func TestExecutionSelectionDurableQueueCaptureAndPassiveResume(t *testing.T) {
	root := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git: %v %s", err, out)
		}
	}
	git("init", "-q")
	if err := os.WriteFile(filepath.Join(root, "baseline.txt"), []byte("baseline\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "base")
	service, err := worktree.Open(root, t.TempDir(), worktree.Options{})
	if err != nil {
		t.Fatal(err)
	}
	artifacts := make([]worktree.Context, 2)
	for i := range artifacts {
		artifacts[i], err = service.Create(t.Context(), worktree.Request{Owner: worktree.Owner{AgentID: "selection-owner", RunID: fmt.Sprintf("selection-%d", i)}, Mode: worktree.Isolated})
		if err != nil {
			t.Fatal(err)
		}
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"unexpected execution\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	home, dir := t.TempDir(), t.TempDir()
	cfg := modelAttributionConfig(server.URL, "selection-model")
	s, err := NewWithSessionDir("selection-resume", cfg, home, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	s.SetWorkDir(root)
	s.SetExecutionService(&launchExecutionFixture{Service: service})
	o, err := s.NewOrchestrator(newMockEventSender())
	if err != nil {
		t.Fatal(err)
	}
	// Use the production prepared-compaction ownership barrier rather than
	// fabricating compaction flags. Its unstarted command must also close safely.
	if err := sessiondb.SaveContextState(s.db, s.sessionID, s.sessionID, sessiondb.ContextState{AgentID: s.sessionID, Generation: 1, Reason: "fixture", Messages: []provider.ChatMessage{provider.NewTextMessage("user", "prior context")}}); err != nil {
		t.Fatal(err)
	}
	prepared, early := s.PrepareCompaction(o.ID)
	if prepared.Run == nil || early.Error != "" {
		t.Fatalf("prepare real compaction: %+v", early)
	}
	queue := func(text string, revision uint64) *InputAdmissionIntent {
		t.Helper()
		intent, err := s.PrepareInputAdmission(o.ID, provider.PlainInput(text), revision)
		if err != nil {
			t.Fatal(err)
		}
		if result := s.CommitInputAdmission(t.Context(), intent); result.Status != InputAdmissionQueued {
			t.Fatalf("queue admission: %+v", result)
		}
		return intent
	}
	logical := queue("captured logical checkout", 1)
	selection, err := s.SelectExecution(t.Context(), artifacts[0].ID, 0)
	if err != nil || selection.Revision != 1 {
		t.Fatalf("select: %+v %v", selection, err)
	}
	isolated := queue("captured first managed checkout", 2)
	selection, err = s.SelectExecution(t.Context(), artifacts[1].ID, 1)
	if err != nil || selection.Revision != 2 {
		t.Fatalf("change selection: %+v %v", selection, err)
	}
	if _, err := s.SelectExecution(t.Context(), artifacts[0].ID, 1); !errors.Is(err, collaboration.ErrStale) {
		t.Fatalf("stale selection accepted: %v", err)
	}
	raw, err := sessiondb.LoadRuntimeState(t.Context(), s.db, executionSelectionKey)
	var saved ExecutionSelection
	if err != nil || json.Unmarshal(raw, &saved) != nil || saved != selection {
		t.Fatalf("selection not durable: %s %v", raw, err)
	}
	// Neither stale writes nor caller-owned projections can mutate the capture.
	projection := s.QueuedInputs()
	projection[1].Input.ControlTarget.ExecutionID = artifacts[1].ID
	path := s.dbPath
	s.Close()
	resumed, err := ResumeSessionWithDir(path, cfg, home, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(resumed.Close)
	if got := resumed.ExecutionSelection(); got != selection {
		t.Fatalf("resume selection: %+v want %+v", got, selection)
	}
	items := resumed.QueuedInputs()
	if len(items) != 2 || items[0].Input.InternalID != logical.InputID || items[1].Input.InternalID != isolated.InputID || items[0].Input.ControlTarget != nil || items[1].Input.ControlTarget.ExecutionID != artifacts[0].ID {
		t.Fatalf("resume rebound queued captures: %+v", items)
	}
	resumed.SetWorkDir(root)
	resumed.SetExecutionService(&launchExecutionFixture{Service: service})
	owner, err := resumed.NewOrchestrator(newMockEventSender())
	if err != nil {
		t.Fatal(err)
	}
	// Exercise the exact dequeue reservation entry point: the already captured
	// nil target is logical-root intent, not permission to select the new B.
	for _, item := range items {
		intent, err := resumed.prepareReservedAdmission(owner.ID, item.Input, item.DraftRevision)
		if err != nil {
			t.Fatal(err)
		}
		got := intent.request.input.ControlTarget
		if item.Input.ControlTarget == nil && got != nil || item.Input.ControlTarget != nil && (got == nil || got.ExecutionID != artifacts[0].ID) {
			t.Fatalf("dequeue reservation rebound capture: %+v", got)
		}
		owner.endTurn(intent.TurnGeneration)
		resumed.finishInputAdmission(intent)
	}
	if calls.Load() != 0 {
		t.Fatalf("passive resume/selection invoked provider: %d", calls.Load())
	}
	for _, original := range artifacts {
		got, err := service.Inspect(original.ID)
		if err != nil || len(got.Pins) != len(original.Pins) || got.Root != original.Root || got.Removed {
			t.Fatalf("passive selection mutated execution: %+v %v", got, err)
		}
	}
}
