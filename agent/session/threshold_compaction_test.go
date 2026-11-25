package session

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"crude/agent/provider"
	"crude/agent/sessiondb"
	"crude/internal/providersim"
)

func TestResponsesThresholdMixedRoundPersistsThroughSessionReopen(t *testing.T) {
	t.Run("full_history", func(t *testing.T) { checkResponsesThresholdMixedReopen(t, false) })
	t.Run("stored_response", func(t *testing.T) { checkResponsesThresholdMixedReopen(t, true) })
}

func checkResponsesThresholdMixedReopen(t *testing.T, store bool) {
	t.Helper()
	const (
		model      = "responses-threshold-session-model"
		mixedText  = "Checking the recorded evidence once."
		callID     = "threshold-session-read"
		fileName   = "threshold-evidence.txt"
		firstInput = "Keep the previous evidence for this request."
		nextInput  = "Check the recorded evidence before answering."
		followUp   = "Use that evidence for this follow-up."
		final      = "The recorded evidence was checked."
	)
	const handoff = "<<<intent>>>\nKeep the recorded evidence.\n<<<work>>>\nThe single directory read completed.\n<<<state>>>\nTicket blue.\n<<<continuation>>>\nAnswer the pending request."
	history := strings.Repeat("prior bulky evidence ", 5200)
	isReadBack := func(r providersim.Request) bool {
		body := simDecodeRequest[struct{ Input []map[string]any }](t, r)
		return len(body.Input) != 0 && body.Input[0]["type"] == "compaction"
	}
	server := providersim.New(t)
	server.Respond(func(r providersim.Request) providersim.Turn {
		switch {
		case strings.HasSuffix(r.Path, "/compact"):
			return providersim.HTTPError(providersim.ModelNotFound)
		case bytes.Contains(r.Raw, []byte(`"compaction_trigger"`)):
			return providersim.HTTPError(providersim.InvalidRequest).WithFaultMessage("unsupported compaction trigger")
		case bytes.Contains(r.Raw, []byte(`"compact_threshold"`)):
			return simTool(callID, "list_directory", `{"path":"."}`).WithText(mixedText).
				WithUsage(providersim.Usage{Input: 31_000, Output: 34})
		case isReadBack(r):
			return providersim.Text(handoff)
		case r.Index == 1:
			return providersim.Text(history).WithUsage(providersim.Usage{Input: 5_000, Output: 28_000})
		default:
			return providersim.Text(final)
		}
	})
	cfg := mediaAttributionConfig(server.BaseURL(providersim.Responses), model, "openai-responses")
	account := cfg.Accounts["account"]
	account.Connection.Protocol = &provider.ProtocolSettings{Store: &store}
	account.Connection.ExtraBody = map[string]any{"context_management": []any{map[string]any{
		"type": "compaction", "compact_threshold": 1000,
	}}}
	cfg.Accounts["account"] = account
	categories := cfg.Agents.Profiles["default"].Categories
	rootConfig := categories[CategoryDefault]
	percent := 20
	rootConfig.ModelContextWindow, rootConfig.ModelMaxOutput, rootConfig.ContextCompactionPercent = 100_000, 32_768, &percent
	categories[CategoryDefault] = rootConfig

	home, dir, project := t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(project, fileName), []byte("ticket blue"), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := NewWithSessionDir("threshold-session", cfg, home, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	s.SetWorkDir(project)
	s.SetYolo(true)
	sender := newMockEventSender()
	root, err := s.NewOrchestrator(sender)
	if err != nil {
		t.Fatal(err)
	}
	run := func(o *Orchestrator, events *mockEventSender, id, text string) {
		t.Helper()
		if !o.SubmitInput(provider.UserInput{InternalID: id, Text: text}) {
			t.Fatalf("could not submit %q", text)
		}
		waitTurnDone(t, events, o.ID, 12*time.Second)
	}
	run(root, sender, "threshold-earlier", firstInput)
	run(root, sender, "threshold-current", nextInput)

	journal := server.Requests()
	if len(journal) != 6 {
		shape := make([]string, 0, len(journal))
		for _, r := range journal {
			body := simDecodeRequest[struct{ Input []map[string]any }](t, r)
			var items []string
			for _, item := range body.Input {
				items = append(items, fmt.Sprintf("%v/%v", item["type"], item["role"]))
			}
			shape = append(shape, fmt.Sprintf("%d %s threshold=%t read-back=%t stream=%t prev=%q items=%v violation=%s",
				r.Index, r.Path, bytes.Contains(r.Raw, []byte(`"compact_threshold"`)), isReadBack(r), r.Stream, r.PreviousResponseID, items, r.Violation))
		}
		if log, err := os.ReadFile(filepath.Join(s.SessionDir(), "session.log")); err == nil {
			for _, line := range strings.Split(string(log), "\n") {
				if strings.Contains(line, "COMPACT") || strings.Contains(line, "checkpoint") {
					t.Log(line)
				}
			}
		}
		t.Fatalf("want seed, both unavailable native routes, threshold, export, and continuation: %v", shape)
	}
	threshold, export, continuation := journal[3], journal[4], journal[5]
	edit := simDecodeRequest[struct {
		ContextManagement []struct {
			Type      string
			Threshold int `json:"compact_threshold"`
		} `json:"context_management"`
	}](t, threshold).ContextManagement
	if !strings.HasSuffix(journal[1].Path, "/compact") || !bytes.Contains(journal[2].Raw, []byte(`"compaction_trigger"`)) ||
		(journal[1].PreviousResponseID != "") != store || len(edit) != 1 || edit[0].Type != "compaction" || edit[0].Threshold != 1000 ||
		!isReadBack(export) || export.OutputLimit == "" {
		t.Fatalf("missing the paid threshold request or its bounded read-back: edits=%v read-back=%t output limit=%q",
			edit, isReadBack(export), export.OutputLimit)
	}
	if store {
		dedicated, alternate := journal[1], journal[2]
		anchor := simStoredResponseID(journal[0])
		first := simDecodeRequest[struct{ Input []any }](t, dedicated).Input
		second := simDecodeRequest[struct{ Input []any }](t, alternate).Input
		if dedicated.PreviousResponseID != anchor || alternate.PreviousResponseID != anchor ||
			len(dedicated.Raw) >= 32<<10 || len(alternate.Raw) >= 32<<10 ||
			len(second) != len(first)+1 || !reflect.DeepEqual(first, second[:len(first)]) ||
			bytes.Contains(threshold.Raw, []byte("prior bulky evidence")) {
			t.Fatal("a definite refusal lost the stored prefix or re-uploaded the large history")
		}
	}
	rows, err := sessiondb.LoadMessagesWithMeta(s.db)
	if err != nil {
		t.Fatal(err)
	}
	var mixed, result provider.ChatMessage
	var calls, results int
	for _, row := range rows {
		for _, call := range row.Msg.ToolCalls {
			if call.ID == callID {
				mixed = row.Msg
				calls++
			}
		}
		if row.Msg.ToolCallID == callID {
			result = row.Msg
			results++
		}
	}
	if calls != 1 || results != 1 || mixed.InternalID == "" || result.InternalID == "" ||
		mixed.ContentString() != mixedText || result.ToolError || !strings.Contains(result.ContentString(), fileName) {
		t.Fatalf("ordinary output or result was not published once: calls=%d results=%d text=%q result=%q",
			calls, results, mixed.ContentString(), result.ContentString())
	}
	wantTypes := []string{"compaction", "message", "function_call"}
	if len(mixed.ProviderItems) != len(wantTypes) {
		t.Fatalf("persisted mixed output has %d provider items", len(mixed.ProviderItems))
	}
	for i, want := range wantTypes {
		if mixed.ProviderItems[i].Type != want {
			t.Fatalf("persisted mixed output item %d is %q, want %q", i, mixed.ProviderItems[i].Type, want)
		}
	}
	for _, r := range []providersim.Request{export, continuation} {
		body := simDecodeRequest[struct{ Input []map[string]any }](t, r)
		start := 0
		if r.Index == continuation.Index {
			start = 1
		}
		if len(body.Input) < start+len(wantTypes)+1 {
			t.Fatalf("request %d lost its mixed suffix", r.Index)
		}
		for i, item := range mixed.ProviderItems {
			var stored map[string]any
			if err := json.Unmarshal(item.Data, &stored); err != nil || !reflect.DeepEqual(stored, body.Input[start+i]) {
				t.Fatalf("request %d changed persisted provider item %d: %v", r.Index, i, err)
			}
		}
		toolResult := body.Input[start+len(wantTypes)]
		if toolResult["type"] != "function_call_output" || toolResult["call_id"] != callID ||
			toolResult["output"] != result.ContentString() || bytes.Count(r.Raw, []byte(callID)) != 2 ||
			bytes.Contains(r.Raw, []byte("prior bulky evidence")) || bytes.Contains(r.Raw, []byte(`"context_management"`)) {
			t.Fatalf("request %d changed or replayed the completed tool round", r.Index)
		}
	}
	wantSystem := simText(threshold.System)
	if store {
		if threshold.PreviousResponseID != simStoredResponseID(journal[0]) {
			t.Fatal("paid threshold did not use the seeded continuation ID")
		}
		// The delta omits the system item. The new request after a checkpoint
		// must send it itself, not infer it from an opaque item in its input.
		wantSystem = simText(journal[0].System)
	}
	if !reflect.DeepEqual(threshold.Tools, continuation.Tools) || wantSystem == "" || simText(continuation.System) != wantSystem {
		t.Fatal("first continuation changed the system contract or tools")
	}
	state, found, err := sessiondb.LoadContextState(s.db, s.sessionID, s.sessionID)
	if err != nil || !found || state.Generation < 2 || !bytes.Contains(adaptiveMessageBytes(t, state.Messages), []byte("Ticket blue.")) {
		t.Fatalf("native checkpoint did not reach durable context: found=%t generation=%d err=%v", found, state.Generation, err)
	}
	records := s.CompactionRecords("")
	if len(records) != 1 || records[0].State != sessiondb.CompactionCompleted || records[0].AgentID != root.ID {
		t.Fatalf("native checkpoint was not committed once: %+v", records)
	}

	path, sessionID := s.dbPath, s.sessionID
	s.Close()
	reopened, err := ResumeSessionWithDir(path, cfg, home, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(reopened.Close)
	reopened.SetWorkDir(project)
	resumedSender := newMockEventSender()
	resumedRoot, err := reopened.NewOrchestrator(resumedSender)
	if err != nil {
		t.Fatal(err)
	}
	resumedState, found, err := sessiondb.LoadContextState(reopened.db, sessionID, sessionID)
	resumedRows, rowsErr := sessiondb.LoadMessagesWithMeta(reopened.db)
	if err != nil || rowsErr != nil || !found || reopened.sessionID != sessionID ||
		!reflect.DeepEqual(state, resumedState) || !bytes.Equal(adaptiveRowsBytes(t, rows), adaptiveRowsBytes(t, resumedRows)) ||
		!reflect.DeepEqual(records, reopened.CompactionRecords("")) || len(server.Requests()) != len(journal) {
		t.Fatalf("passive reopen changed persisted identities or requested new work: context=%v rows=%v", err, rowsErr)
	}
	run(resumedRoot, resumedSender, "threshold-followup", followUp)
	after := server.Requests()
	if len(after) != len(journal)+1 {
		t.Fatalf("follow-up issued %d requests, want one", len(after)-len(journal))
	}
	last := after[len(after)-1]
	var visible strings.Builder
	for _, msg := range last.Effective.Messages {
		visible.WriteString(simText(msg.Parts))
	}
	var seal struct{ ID string }
	if err := json.Unmarshal(mixed.ProviderItems[0].Data, &seal); err != nil || seal.ID == "" {
		t.Fatalf("persisted checkpoint has no provider identity: %v", err)
	}
	carriedCheckpoint := strings.Contains(visible.String(), "Ticket blue.") || bytes.Contains(last.Raw, []byte(seal.ID)) ||
		last.PreviousResponseID == simStoredResponseID(continuation)
	if !bytes.Contains(last.Raw, []byte(followUp)) || !carriedCheckpoint || !reflect.DeepEqual(last.Tools, continuation.Tools) ||
		bytes.Contains(last.Raw, []byte("prior bulky evidence")) || bytes.Contains(last.Raw, []byte(`"context_management"`)) {
		t.Fatalf("first request after reopen lost the committed checkpoint or contract: checkpoint=%t previous=%q",
			carriedCheckpoint, last.PreviousResponseID)
	}
}
