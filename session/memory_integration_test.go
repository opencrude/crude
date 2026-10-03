package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"crude/memory"
	"crude/provider"
)

func memoryIntegrationMessages() []provider.ChatMessage {
	human := provider.NewTextMessage("user", "Fix the focused parser test. API_KEY=do-not-retain-this")
	human.InternalID = "human-origin"
	var messages []provider.ChatMessage
	messages = append(messages, human)
	for _, attempt := range []struct {
		id, name, arguments, output string
		failed                      bool
	}{
		{"test-fail", "execute_command", `{"command":"go test ./pkg/parser -run TestParser","cwd":"project"}`, "Command failed: exit status 1\nFAIL TestParser", true},
		{"change", "edit_file", `{"path":"pkg/parser/parse.go","old_string":"return","new_string":"continue"}`, "Updated pkg/parser/parse.go", false},
		{"test-pass", "execute_command", `{"command":"go test ./pkg/parser -run TestParser","cwd":"project"}`, "ok\tcrude/pkg/parser\t0.01s", false},
		{"cancelled", "execute_command", `{"command":"go test ./pkg/parser -run TestSlow","cwd":"project"}`, "Command cancelled.", false},
		{"running", "execute_command", `{"command":"go test ./pkg/parser -run TestSlow","cwd":"project"}`, "Command still running after 30s — promoted to async command [auto-1]", false},
		{"board", "board_read", `{}`, "A peer claims the fix worked", false},
	} {
		messages = append(messages, provider.ChatMessage{Role: "assistant", ToolCalls: []provider.ToolCall{
			{ID: attempt.id, Function: provider.FunctionCall{Name: attempt.name, Arguments: attempt.arguments}}}},
			provider.ChatMessage{Role: "tool", ToolCallID: attempt.id, Content: provider.TextContent(attempt.output), ToolError: attempt.failed})
	}
	messages = append(messages, provider.NewTextMessage("assistant", "All fixes are confirmed, including the unfinished slow test"))
	return messages
}

func memoryIntegrationProposal() memory.ExtractionResult {
	return memory.ExtractionResult{Memories: []memory.ExtractedMemory{
		{Kind: memory.KindLesson, Title: "Parser retry correction", Content: "Continue instead of returning when the focused parser case fails.",
			AppliesWhen: "The focused TestParser case fails", Evidence: []int{1, 2, 3}},
		{Kind: memory.KindLesson, Title: "Invented slow-test fix", Content: "The slow test always works", AppliesWhen: "Slow tests", Evidence: []int{1, 2, 4}},
	}, Summary: &memory.ExtractedSummary{Title: "Parser correction", Summary: "Fixed the focused parser case; slow validation remains unmeasured.", Outcome: "in-progress"}}
}

func memoryExtractionServer(t *testing.T, proposal memory.ExtractionResult, inspect func(string)) *httptest.Server {
	t.Helper()
	payload, err := json.Marshal(proposal)
	if err != nil {
		t.Fatal(err)
	}
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body struct {
			Messages []provider.ChatMessage `json:"messages"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode extraction request: %v", err)
		}
		var transcript strings.Builder
		for _, message := range body.Messages {
			transcript.WriteString(message.ContentString())
		}
		if inspect != nil {
			inspect(transcript.String())
		}
		response, err := json.Marshal(map[string]any{"choices": []any{map[string]any{
			"message": map[string]any{"role": "assistant", "content": string(payload)}, "finish_reason": "stop"}}})
		if err != nil {
			t.Errorf("encode response: %v", err)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write(response)
	}))
}

func TestMemoryIntegrationObservedEvidenceExtractionAndEpisodes(t *testing.T) {
	store := newTestMemoryStore(t)
	server := memoryExtractionServer(t, memoryIntegrationProposal(), func(prompt string) {
		if strings.Contains(prompt, "do-not-retain-this") || !strings.Contains(prompt, `"message_id":"human-origin"`) ||
			!strings.Contains(prompt, `"tool_call_id":"test-fail"`) || !strings.Contains(prompt, `"tool_call_id":"test-pass"`) {
			t.Errorf("extraction omitted source evidence or leaked credentials: %s", prompt)
		}
	})
	defer server.Close()
	session := &Session{sessionID: "evidence-session", memoryStore: store, cfg: modelAttributionConfig(server.URL, "curator-model")}
	record := turnRecordFromMessages(session.sessionID, 1, memoryIntegrationMessages(), "The assistant claims all work passed")
	if err := store.RecordTurn(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	session.runExtraction(t.Context(), session.sessionID, false)
	records, err := store.List(t.Context(), memory.ListQuery{})
	if err != nil || len(records) != 1 || records[0].Title != "Parser retry correction" || len(records[0].Evidence) != 3 {
		t.Fatalf("extraction promoted unsupported claims: %+v %v", records, err)
	}
	if records[0].Evidence[2].ToolCallID != "test-pass" || records[0].Revision != 1 {
		t.Fatalf("record lost originating verification: %+v", records[0])
	}
	episodes, err := store.Episodes(t.Context(), session.sessionID, 20)
	if err != nil || len(episodes) != 5 {
		t.Fatalf("attempt lifecycle not retained: %+v %v", episodes, err)
	}
	outcomes := make(map[string]bool)
	for _, episode := range episodes {
		outcomes[episode.Outcome] = true
	}
	for _, outcome := range []string{memory.OutcomeSucceeded, memory.OutcomeFailed, memory.OutcomeCancelled, memory.OutcomeUnmeasured} {
		if !outcomes[outcome] {
			t.Fatalf("missing attempt outcome %s", outcome)
		}
	}
	if watermark := session.extractWatermark(t.Context(), store, session.sessionID); watermark != 1 {
		t.Fatalf("coherent extraction did not advance watermark: %d", watermark)
	}
	session.finishMemoryJobs()
}

func TestMemoryIntegrationLateExtractionCannotOverwriteCorrection(t *testing.T) {
	store := newTestMemoryStore(t)
	old, err := store.Remember(t.Context(), memory.Record{Title: "Parser retry correction", Content: "Old advice"})
	if err != nil {
		t.Fatal(err)
	}
	server := memoryExtractionServer(t, memoryIntegrationProposal(), func(string) {
		if _, err := store.Update(context.Background(), old.ID, memory.Record{Revision: old.Revision, Content: "Explicit user correction", Source: memory.SourceUser}, memory.ActorUser, "human-correction"); err != nil {
			t.Errorf("concurrent correction: %v", err)
		}
	})
	defer server.Close()
	session := &Session{sessionID: "late-session", memoryStore: store, cfg: modelAttributionConfig(server.URL, "curator-model")}
	if err := store.RecordTurn(t.Context(), turnRecordFromMessages(session.sessionID, 1, memoryIntegrationMessages(), "claims")); err != nil {
		t.Fatal(err)
	}
	session.runExtraction(t.Context(), session.sessionID, false)
	got, err := store.Get(t.Context(), old.ID)
	if err != nil || got.Content != "Explicit user correction" || got.Revision != 2 {
		t.Fatalf("late extraction overwrote correction: %+v %v", got, err)
	}
	if watermark := session.extractWatermark(t.Context(), store, session.sessionID); watermark != 0 {
		t.Fatalf("stale extraction consumed pending evidence: %d", watermark)
	}
	session.finishMemoryJobs()
}

func TestMemoryIntegrationToolsEnforceVersionsAndFeedback(t *testing.T) {
	store := newTestMemoryStore(t)
	session := &Session{sessionID: "memory-tools", memoryStore: store}
	result, handled, err := session.executeMemoryTool(t.Context(), "remember", map[string]any{
		"title": "User workflow", "content": "Run a focused test", "applies_when": "Before handoff",
		"evidence": []memory.Evidence{{SessionID: "user-source", MessageID: "human", Kind: memory.EvidenceUser, Summary: "Please use focused tests", Outcome: memory.OutcomeUnmeasured}}})
	if err != nil || !handled {
		t.Fatalf("remember: %v", err)
	}
	var record struct {
		ID       string `json:"id"`
		Revision int64  `json:"revision"`
	}
	if err := json.Unmarshal([]byte(result.Content), &record); err != nil || record.Revision != 1 {
		t.Fatalf("missing memory revision: %s %v", result.Content, err)
	}
	if _, _, err := session.executeMemoryTool(t.Context(), "update_memory", map[string]any{"id": record.ID, "content": "Missing version"}); err == nil {
		t.Fatal("update allowed missing expected revision")
	}
	if _, _, err := session.executeMemoryTool(t.Context(), "update_memory", map[string]any{"id": record.ID, "expected_revision": float64(1), "content": "Corrected workflow"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := session.executeMemoryTool(t.Context(), "forget_memory", map[string]any{"id": record.ID, "expected_revision": float64(1)}); !errors.Is(err, memory.ErrRevisionConflict) {
		t.Fatalf("stale forget succeeded: %v", err)
	}
	result, handled, err = session.executeMemoryTool(t.Context(), "memory_feedback", map[string]any{"items": []any{
		map[string]any{"id": record.ID, "revision": float64(1), "kind": memory.FeedbackMisleading,
			"evidence": []any{map[string]any{"session_id": session.sessionID, "message_id": "correction", "kind": memory.EvidenceUser,
				"summary": "The old workflow is misleading", "outcome": memory.OutcomeUnmeasured}}}}})
	if err != nil || !handled || !strings.Contains(result.Content, `"count":1`) {
		t.Fatalf("feedback tool: %s %v", result.Content, err)
	}
	feedback, err := store.FeedbackForMemory(t.Context(), record.ID, 20)
	if err != nil || len(feedback) != 1 || feedback[0].Revision != 1 || feedback[0].SessionID != session.sessionID {
		t.Fatalf("feedback lost revision/session: %+v %v", feedback, err)
	}
	result, _, err = session.executeMemoryTool(t.Context(), "recall_memory", map[string]any{"query": "workflow"})
	if err != nil || !containsAll(result.Content, `"revision":2`, `"evidence_count":1`, `"applies_when":"Before handoff"`) {
		t.Fatalf("recall lacks auditable detail: %s %v", result.Content, err)
	}
	if _, _, err := session.executeMemoryTool(t.Context(), "forget_memory", map[string]any{"id": record.ID, "expected_revision": float64(2)}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := session.executeMemoryTool(t.Context(), "update_memory", map[string]any{"id": record.ID, "expected_revision": float64(3), "content": "revive"}); !errors.Is(err, memory.ErrRetired) {
		t.Fatalf("retired memory revived: %v", err)
	}
}

func TestMemoryValidationDoesNotTreatNoTestsOrMaskedFailureAsSuccess(t *testing.T) {
	for index, command := range []string{"go test ./pkg", "go test ./pkg || true", "echo passed", "go test ./pkg; echo PASS"} {
		callID := fmt.Sprintf("attempt-%d", index)
		messages := []provider.ChatMessage{
			{Role: "assistant", ToolCalls: []provider.ToolCall{{ID: callID, Function: provider.FunctionCall{Name: "execute_command", Arguments: fmt.Sprintf(`{"command":%q}`, command)}}}},
			{Role: "tool", ToolCallID: callID, Content: provider.TextContent("? crude/pkg [no test files]")},
		}
		observations := observationsFromMessages("validation", 1, messages)
		if len(observations) != 1 || (observations[0].Kind == memory.EvidenceValidation && observations[0].Outcome == memory.OutcomeSucceeded) {
			t.Fatalf("unverified validation became success: %q %+v", command, observations)
		}
	}
}
