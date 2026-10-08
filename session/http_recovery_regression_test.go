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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"crude/agent"
	"crude/collaboration"
	"crude/provider"
	"crude/sessiondb"
	recoverytypes "crude/types/recovery"
)

// The gateway speaks Chat Completions, but wraps an upstream Responses
// disconnect in an HTTP error. An SSE-only fix cannot recover this incident.
func TestHTTPGatewayDisconnectRecoveryDurableOwners(t *testing.T) {
	for _, owner := range []string{"root", "child"} {
		for _, stop := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stop=%t", owner, stop), func(t *testing.T) {
				checkHTTPGatewayDisconnectRecovery(t, owner, stop)
			})
		}
	}
}

func checkHTTPGatewayDisconnectRecovery(t *testing.T, owner string, stop bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	defer cancel()
	home, sessions, project := t.TempDir(), t.TempDir(), t.TempDir()
	model := fmt.Sprintf("http-disconnect-%s-%t", owner, stop)
	parentModel := model + "-parent"
	original := "retain exact HTTP gateway assignment"
	path := filepath.Join(project, "effect.txt")
	const content = "completed effect before gateway disconnect\n"
	const callID = "completed-effect"
	var parentID string
	var requests atomic.Int32
	var mu sync.Mutex
	var replacementAt time.Time
	fixture := sessionSupervisedProtocolFixtures()["openai"]
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var raw json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			t.Errorf("decode local HTTP request: %v", err)
			return
		}
		var body struct{ Model string }
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if body.Model == parentModel {
			_, _ = fmt.Fprint(w, fixture.reply("parent seed complete", "", ""))
			return
		}
		if body.Model != model || r.URL.Path != "/v1/chat/completions" {
			t.Errorf("wrong actual gateway adapter: model=%q path=%q", body.Model, r.URL.Path)
		}
		switch index := requests.Add(1); index {
		case 1:
			args, err := json.Marshal(map[string]string{"path": path, "content": content})
			if err != nil {
				t.Error(err)
				return
			}
			_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":%q,\"type\":\"function\",\"function\":{\"name\":\"write_file\",\"arguments\":%q}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n", callID, string(args))
		case 2:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusRequestTimeout)
			_, _ = fmt.Fprint(w, `{"error":{"type":"invalid_request_error","message":"stream error: stream disconnected before completion: stream closed before response.completed"}}`)
		default:
			if stop {
				t.Error("cancelled cooldown restarted HTTP execution")
			}
			mu.Lock()
			if replacementAt.IsZero() {
				replacementAt = time.Now()
			}
			mu.Unlock()
			wire := string(raw)
			if strings.Count(wire, original) != 1 || !strings.Contains(wire, callID) || !strings.Contains(wire, "runtime_recovery") {
				t.Errorf("replacement lost accepted input/tool evidence or recovery control")
			}
			if owner == "child" && index == 3 {
				args, err := json.Marshal(map[string]any{"recipient_ids": []string{parentID}, "kind": "final", "summary": "Gateway recovered", "body": "The completed effect was retained exactly once."})
				if err != nil {
					t.Error(err)
					return
				}
				_, _ = fmt.Fprint(w, fixture.reply("", "gateway-final-report", string(args)))
			} else {
				_, _ = fmt.Fprint(w, fixture.reply("gateway task completed", "", ""))
			}
		}
	}))
	t.Cleanup(server.Close)
	rootModel := model
	if owner == "child" {
		rootModel = parentModel
	}
	cfg := modelAttributionConfig(server.URL, rootModel)
	account := cfg.Accounts["account"]
	account.Connection.APIStandard = "openai"
	// Exhaust the request budget immediately; the TASK supervisor, not a
	// transport retry or manual Retry now, must own the next request.
	account.Connection.Dialer = &provider.DialSettings{MaxAttempts: 1}
	cfg.Accounts["account"] = account
	profile := cfg.Agents.Profiles[cfg.Agents.ActiveProfile]
	on, zero := true, 0
	profile.MultiAgent = &on
	category := profile.Categories[CategoryDefault]
	category.ContextCompactionPercent, category.ModelSupportsTools = &zero, true
	category.AllowedTools = []string{"write_file"}
	profile.Categories[CategoryDefault] = category
	category.Model, category.AllowedTools = model, []string{"write_file", "publish_report"}
	profile.Categories[CategoryCrafter] = category
	cfg.Agents.Enabled, cfg.Agents.MaxConcurrent = true, 1
	cfg.Agents.Profiles[cfg.Agents.ActiveProfile] = profile
	s, err := NewWithSessionDir("", cfg, home, sessions)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	s.SetWorkDir(project)
	s.SetYolo(true)
	journal := sessiondb.OpenAndMigrateAt(t.TempDir())
	t.Cleanup(func() { _ = journal.Close() })
	store, err := collaboration.Attach(journal, project, filepath.Join(home, "locks"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close(); _ = store.Close() })
	s.SetCollaboration(store, "http-recovery-fixture")
	sender := &sessionSupervisedJourneySender{mockEventSender: newMockEventSender()}
	type evidence struct {
		snapshot sessionSupervisedJourneySnapshot
		err      error
	}
	observed := make(chan evidence, 1)
	var once sync.Once
	sender.observe = func(typ string, data any, id string) {
		status, ok := data.(recoverytypes.Status)
		if typ != "recovery" || !ok || status.State != recoverytypes.NeedsAttention && (status.State != recoverytypes.Recovering || status.RetryAt.IsZero()) {
			return
		}
		once.Do(func() {
			ownerPath, sessionID := s.dbPath, s.sessionID
			if owner == "child" {
				ownerPath, sessionID = filepath.Join(s.SessionDir(), "subagents", id+".db"), id
			}
			snapshot, loadErr := loadSessionSupervisedJourneySnapshot(ctx, ownerPath, sessionID, sessionID, status.ID)
			observed <- evidence{snapshot, loadErr}
		})
	}
	orch, err := s.NewOrchestrator(sender)
	if err != nil {
		t.Fatal(err)
	}
	parentID = orch.ID
	ownerID, ownerSessionID, ownerPath := orch.ID, s.sessionID, s.dbPath
	var runner *SubAgentRunner
	if owner == "child" {
		if !orch.SubmitInput(provider.PlainInput("seed parent")) {
			t.Fatal("parent admission rejected")
		}
		waitTransitionTurn(t, sender.mockEventSender, orch)
		if !s.initSubAgentRunner() {
			t.Fatal("session child runner unavailable")
		}
		runner = s.SubAgentRunner()
		// Prevent the report notification starting an unrelated parent turn.
		s.CloseOrchestrator(orch.ID)
		waitSessionSupervisedJourney(t, "parent input loop close", s.orchWg.Wait)
		result, startErr := runner.StartAgent(ctx, map[string]any{"category": CategoryCrafter, "description": original, "allowed_tools": []string{"write_file", "publish_report"}})
		if startErr != nil {
			t.Fatal(startErr)
		}
		var identity struct {
			AgentID string `json:"agent_id"`
		}
		if err := json.Unmarshal([]byte(result.Content), &identity); err != nil || identity.AgentID == "" {
			t.Fatalf("child launch identity: %s err=%v", result.Content, err)
		}
		ownerID, ownerSessionID = identity.AgentID, identity.AgentID
		ownerPath = filepath.Join(s.SessionDir(), "subagents", ownerID+".db")
	} else if !orch.SubmitInput(provider.PlainInput(original)) {
		t.Fatal("root admission rejected")
	}
	var first evidence
	select {
	case first = <-observed:
	case <-ctx.Done():
		t.Fatalf("no durable countdown after exhausted HTTP request: requests=%d", requests.Load())
	}
	if first.err != nil {
		t.Fatal(first.err)
	}
	cp := first.snapshot.checkpoint
	assertHTTPRecoveryEffect(t, cp.ActiveContext, path, content, callID)
	if cp.Status.State != recoverytypes.Recovering || cp.Status.RetryAt.IsZero() || !cp.Status.RetryAt.After(cp.Status.UpdatedAt) || cp.Status.RetryAt.Sub(cp.Status.UpdatedAt) > 5*time.Minute {
		t.Fatalf("HTTP 408 gateway disconnect parked instead of automatic countdown: %+v", cp.Status)
	}
	if requests.Load() != 2 || cp.Status.AgentID != ownerID || cp.Status.Generation != 1 || cp.Status.ID == "" || !cp.InputAdmitted || cp.ContextOwnerID != ownerSessionID || cp.PendingInput.InternalID == "" || !strings.Contains(cp.PendingInput.Text, original) || cp.ContextRevision != first.snapshot.context.Revision || !reflect.DeepEqual(cp.ActiveContext, first.snapshot.context.Messages) {
		t.Fatalf("countdown lost exact durable ownership/source: requests=%d checkpoint=%+v", requests.Load(), cp)
	}
	if owner == "child" {
		mirror, found, err := sessiondb.LoadRecoveryCheckpoint(s.db, s.sessionID, ownerID)
		if err != nil || !found || mirror.Status.ID != cp.Status.ID || mirror.Status.Generation != cp.Status.Generation || !reflect.DeepEqual(mirror.ActiveContext, cp.ActiveContext) {
			t.Fatalf("child countdown lacks exact parent mirror: found=%t err=%v", found, err)
		}
	}
	if stop && !s.CancelRecoveryFor(ownerID, cp.Status.ID, cp.Status.Generation) {
		t.Fatal("exact cooldown cancellation rejected")
	}
	// No RetryRecoveryFor, wake signal or configuration edit anywhere in this
	// journey: the real one-second supervisor cooldown must expire itself.
	wantReason := "completed"
	if stop {
		wantReason = "cancelled"
	}
	settled := false
	for !settled {
		select {
		case event := <-sender.ch:
			if event.agentID != ownerID {
				continue
			}
			if event.typ == "recovery" && event.data.(recoverytypes.Status).State == recoverytypes.NeedsAttention {
				t.Fatalf("HTTP recovery unexpectedly parked: %+v", event.data)
			}
			if event.typ == "done" {
				data, _ := event.data.(map[string]any)
				if data["reason"] != wantReason || !stop && requests.Load() < 3 {
					t.Fatalf("false terminal event before recovered success: %+v requests=%d", data, requests.Load())
				}
				settled = true
			}
		case <-ctx.Done():
			t.Fatalf("automatic HTTP recovery never settled: requests=%d", requests.Load())
		}
	}
	if runner != nil {
		waitSessionSupervisedJourney(t, "child settlement", runner.Wait)
	}
	if stop {
		// Observe beyond the abandoned cooldown, proving no delayed restart.
		select {
		case <-time.After(time.Until(cp.Status.RetryAt) + 200*time.Millisecond):
		case <-ctx.Done():
			t.Fatal("cancelled cooldown observation timed out")
		}
	} else {
		mu.Lock()
		resumedAt := replacementAt
		mu.Unlock()
		if resumedAt.Before(cp.Status.RetryAt) {
			t.Fatalf("HTTP replacement bypassed automatic countdown: %s < %s", resumedAt, cp.Status.RetryAt)
		}
	}
	wantRequests, wantTools := int32(3), int32(1)
	if owner == "child" && !stop {
		wantRequests, wantTools = 4, 2 // completed file effect plus required report
	}
	if stop {
		wantRequests = 2
	}
	if requests.Load() != wantRequests || sender.toolStarts.Load() != wantTools || sender.toolResults.Load() != wantTools {
		t.Fatalf("replayed/lost effects: requests=%d/%d tools=%d/%d results=%d", requests.Load(), wantRequests, sender.toolStarts.Load(), wantTools, sender.toolResults.Load())
	}
	terminal, err := loadSessionSupervisedJourneySnapshot(ctx, ownerPath, ownerSessionID, ownerSessionID, cp.Status.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantState, wantTurn := recoverytypes.Recovered, sessiondb.TurnCompleted
	if stop {
		wantState, wantTurn = recoverytypes.Cancelled, sessiondb.TurnInterrupted
	}
	if terminal.checkpoint.Status.State != wantState || !terminal.checkpoint.Status.RetryAt.IsZero() || terminal.checkpoint.Status.AgentID != ownerID || terminal.checkpoint.Status.Generation != cp.Status.Generation || terminal.turn == nil || terminal.turn.AgentID != ownerID || terminal.turn.State != wantTurn {
		t.Fatalf("terminal event preceded durable exact-owner settlement: recovery=%+v turn=%+v", terminal.checkpoint.Status, terminal.turn)
	}
	assertSessionSupervisedJourneyIdentities(t, first.snapshot, terminal)
	assertHTTPRecoveryEffect(t, terminal.context.Messages, path, content, callID)
	if owner == "child" && !stop {
		result, found := runner.Manager().GetCompleted(agent.AgentID(ownerID))
		if !found || result.Status != agent.SubAgentCompleted || result.ReportID == "" || result.ReportRevision != 1 {
			t.Fatalf("recovered child completed without mandatory report: %+v", result)
		}
		artifact, err := store.Artifact(ctx, result.ReportID, result.ReportRevision)
		if err != nil || artifact.AuthorID != ownerID || artifact.Kind != collaboration.ReportFinal || artifact.RunID == "" {
			t.Fatalf("recovered child report provenance: %+v err=%v", artifact, err)
		}
		run, err := store.Run(ctx, artifact.RunID)
		if err != nil || run.AgentID != ownerID || run.State != collaboration.RunSucceeded {
			t.Fatalf("report publication did not settle actual child run: %+v err=%v", run, err)
		}
		page, err := store.Page(ctx, collaboration.PageQuery{RecipientID: parentID, Kind: collaboration.EventReportPublished})
		if err != nil || len(page.Events) != 1 || page.Events[0].SenderID != ownerID || page.Events[0].RunID != artifact.RunID {
			t.Fatalf("final report not addressed exactly once to parent: %+v err=%v", page, err)
		}
	}
}

func assertHTTPRecoveryEffect(t *testing.T, messages []provider.ChatMessage, path, content, callID string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil || string(data) != content {
		for _, message := range messages {
			if message.ToolCallID == callID {
				t.Logf("completed effect result: error=%t content=%s", message.ToolError, message.ContentString())
			}
		}
		t.Fatalf("completed tool effect missing/changed: %q err=%v", data, err)
	}
	calls, results := 0, 0
	for _, message := range messages {
		for _, call := range message.ToolCalls {
			if call.ID == callID && call.Function.Name == "write_file" {
				calls++
			}
		}
		if message.Role == "tool" && message.ToolCallID == callID && !message.ToolError {
			results++
		}
	}
	if calls != 1 || results != 1 {
		t.Fatalf("completed effect not retained exactly once: calls=%d results=%d", calls, results)
	}
}
