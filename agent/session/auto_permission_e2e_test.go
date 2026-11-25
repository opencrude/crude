package session

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"crude/agent/provider"
	"crude/agent/sessiondb"
	"crude/internal/providersim"
	"crude/types/setting"
)

func TestAutoPermissionRealTurnWritesAndPersistsReviewProvenance(t *testing.T) {
	var rootCalls, reviewCalls atomic.Int32
	server := providersim.New(t)
	server.Respond(func(request providersim.Request) providersim.Turn {
		if request.Model == "review-model" {
			reviewCalls.Add(1)
			return providersim.Text(`{"decision":"allow","reason":"routine file creation"}`).WithUsage(providersim.Usage{Input: 80, Output: 12})
		}
		if rootCalls.Add(1) == 1 {
			return providersim.CallTools(providersim.ToolCall{ID: "create-1", Name: "write_file", Arguments: `{"path":"hello.txt","content":"hello from reviewed tool"}`}).WithUsage(providersim.Usage{Input: 100, Output: 20})
		}
		return providersim.Text("created").WithUsage(providersim.Usage{Input: 140, Output: 5})
	})
	cfg := modelAttributionConfig(server.BaseURL(providersim.Chat), "main-model")
	cfg.Safety.AutoPermission = setting.AutoPermission{Enabled: "on", Provider: "openrouter", AccountID: "account", Model: "review-model"}
	home, directory, root := t.TempDir(), t.TempDir(), t.TempDir()
	s, err := NewWithSessionDir("review-e2e", cfg, home, directory)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.SetWorkDir(root)
	attachDelegationTestCollaboration(t, s)
	sender := newMockEventSender()
	orch, err := s.NewOrchestrator(sender)
	if err != nil {
		t.Fatal(err)
	}
	if !orch.SubmitInput(provider.PlainInput("Create hello.txt")) {
		t.Fatal("turn not admitted")
	}
	deadline := time.After(5 * time.Second)
	done := false
	for !done {
		select {
		case evt := <-sender.ch:
			if evt.typ == "blocking" {
				t.Fatal("approved operation still opened human permission prompt")
			}
			if evt.typ == "error" {
				t.Fatalf("turn error: %v", evt.data)
			}
			if evt.typ == "tool_result" {
				if result, ok := evt.data.(map[string]any); ok && result["error"] == "true" {
					t.Fatalf("reviewed tool failed: %v", result["result"])
				}
			}
			done = evt.typ == "done"
		case <-deadline:
			t.Fatal("reviewed turn did not complete")
		}
	}
	waitAssistantCount(t, s, 1)
	data, err := os.ReadFile(filepath.Join(root, "hello.txt"))
	if err != nil || string(data) != "hello from reviewed tool" {
		t.Fatalf("reviewed file=%q err=%v", data, err)
	}
	if reviewCalls.Load() != 1 {
		t.Fatalf("review requests=%d", reviewCalls.Load())
	}
	assertReview := func(messages []sessiondb.MessageWithMeta) {
		t.Helper()
		found := false
		for _, message := range messages {
			for _, snapshot := range message.Meta.Snapshots {
				if snapshot.ToolName == "write_file" && snapshot.PermTag == "reviewer-allowed" {
					found = true
				}
			}
		}
		if !found {
			t.Fatal("reviewer approval provenance absent from conversation")
		}
	}
	assertReview(s.MessagesWithMeta())
	records, err := sessiondb.ListUsageRecords(s.DB())
	if err != nil {
		t.Fatal(err)
	}
	foundReview := false
	for _, record := range records {
		if record.RequestKind == "permission_review" {
			foundReview = true
			if record.ModelID != "review-model" || record.ContextUsed != 0 || record.PromptTokens != 80 {
				t.Fatalf("review ledger=%+v", record)
			}
		}
	}
	if !foundReview {
		t.Fatal("reviewer cost missing from durable usage ledger")
	}
	dbPath := filepath.Join(directory, s.SessionID()+".db")
	s.Close()
	resumed, err := ResumeSessionWithDir(dbPath, cfg, home, directory)
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()
	assertReview(resumed.MessagesWithMeta())
}
