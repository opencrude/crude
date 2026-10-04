package session

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"crude/provider"
	"crude/sessiondb"
	"crude/types"
)

func TestAutoPermissionRealTurnWritesAndPersistsReviewProvenance(t *testing.T) {
	var rootCalls, reviewCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request provider.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		if request.Model == "review-model" {
			reviewCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"{\"decision\":\"allow\",\"reason\":\"routine file creation\"}"},"finish_reason":"stop"}],"usage":{"prompt_tokens":80,"completion_tokens":12}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if rootCalls.Add(1) == 1 {
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"create-1\",\"type\":\"function\",\"function\":{\"name\":\"write_file\",\"arguments\":\"{\\\"path\\\":\\\"hello.txt\\\",\\\"content\\\":\\\"hello from reviewed tool\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}],\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":20}}\n\ndata: [DONE]\n\n")
			return
		}
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"created\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":140,\"completion_tokens\":5}}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	cfg := modelAttributionConfig(server.URL, "main-model")
	cfg.Safety.AutoPermission = types.AutoPermissionSettings{Enabled: "on", Provider: "openrouter", AccountID: "account", Model: "review-model"}
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
