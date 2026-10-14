package session

import (
	"bytes"
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

func TestRegisteredProtocolsSessionSupervisedPartialToolRecovery(t *testing.T) {
	runSessionSupervisedProtocolMatrix(t, []string{"complete"})
}

func TestRegisteredProtocolsSessionSupervisedStopDuringCooldown(t *testing.T) {
	runSessionSupervisedProtocolMatrix(t, []string{"stop"})
}

func TestRegisteredProtocolsSessionPermanentFailuresNeedAttention(t *testing.T) {
	runSessionSupervisedProtocolMatrix(t, []string{"schema", "auth", "quota"})
}

func runSessionSupervisedProtocolMatrix(t *testing.T, outcomes []string) {
	t.Helper()
	fixtures := sessionSupervisedProtocolFixtures()
	protocols := provider.ProtocolList()
	if len(protocols) == 0 || len(fixtures) != len(protocols) {
		t.Fatalf("fixtures=%d registered protocols=%d", len(fixtures), len(protocols))
	}
	for _, protocol := range protocols {
		id := protocol.Info().ID
		fixture, ok := fixtures[id]
		if !ok {
			t.Fatalf("missing session-owner supervised fixture for registered protocol %q", id)
		}
		for _, owner := range []string{"root", "child"} {
			for _, outcome := range outcomes {
				t.Run(id+"/"+owner+"/"+outcome, func(t *testing.T) {
					t.Parallel()
					checkSessionSupervisedProtocolJourney(t, id, owner, outcome, fixture)
				})
			}
		}
	}
}

// Observe the real committed boundary before the supervisor can update its row
// again. Only event delivery is instrumented; persistence, clocks and waits are real.
type sessionSupervisedJourneySender struct {
	*mockEventSender
	observe                           func(string, any, string)
	toolStarts, toolResults, children atomic.Int32
}

func (s *sessionSupervisedJourneySender) SendAgentEvent(typ string, data any) {
	s.SendAgentEventWithID(typ, data, "")
}

func (s *sessionSupervisedJourneySender) SendAgentEventWithID(typ string, data any, id string) {
	switch typ {
	case "tool_call":
		s.toolStarts.Add(1)
	case "tool_result":
		s.toolResults.Add(1)
	case "sub_agent_started":
		s.children.Add(1)
	}
	if s.observe != nil {
		s.observe(typ, data, id)
	}
	s.mockEventSender.SendAgentEventWithID(typ, data, id)
}

func (s *sessionSupervisedJourneySender) SendAgentEventEnvelope(event agent.AgentEvent) {
	s.SendAgentEventWithID(event.Type, event.Data, event.AgentID)
}

type sessionSupervisedJourneySnapshot struct {
	checkpoint sessiondb.RecoveryCheckpoint
	context    sessiondb.ContextState
	transcript []sessiondb.MessageWithMeta
	identities map[string]string
	turn       *sessiondb.TurnState
}

func loadSessionSupervisedJourneySnapshot(ctx context.Context, path, sessionID, ownerID, incidentID string) (sessionSupervisedJourneySnapshot, error) {
	var snapshot sessionSupervisedJourneySnapshot
	db, err := sessiondb.Open(path)
	if err != nil {
		return snapshot, err
	}
	defer db.Close()
	state, found, err := sessiondb.LoadContextState(db, sessionID, ownerID)
	if err != nil || !found {
		return snapshot, fmt.Errorf("load context %s/%s: found=%t err=%v", sessionID, ownerID, found, err)
	}
	snapshot.context = state
	snapshot.transcript, err = sessiondb.LoadMessagesWithMeta(db)
	if err != nil {
		return snapshot, err
	}
	snapshot.turn, _, err = sessiondb.LatestTurnStates(ctx, db, sessionID)
	if err != nil {
		return snapshot, err
	}
	records, err := sessiondb.LoadRecoveryRecordsForOwner(db, sessionID, ownerID)
	if err != nil {
		return snapshot, fmt.Errorf("exact owner recovery records = %+v, err=%v", records, err)
	}
	if incidentID == "" {
		// A normal Stop has a durable boundary/ledger, not a recovery incident.
		// Its hidden unfinished checkpoint must also have been discarded.
		var checkpoints int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM agent_recoveries WHERE session_id=? AND context_owner_id=?`, sessionID, ownerID).Scan(&checkpoints); err != nil {
			return snapshot, err
		}
		if len(records) != 0 || checkpoints != 0 {
			return snapshot, fmt.Errorf("normal Stop retained recovery evidence: records=%+v checkpoints=%d", records, checkpoints)
		}
	} else {
		if len(records) != 1 || records[0].ID != incidentID {
			return snapshot, fmt.Errorf("exact owner recovery records = %+v", records)
		}
		snapshot.checkpoint.Status = records[0]
		var pending, archive, active []byte
		if err := db.QueryRowContext(ctx, `SELECT pending_input_json,input_admitted,archive_json,active_context_json,
			context_owner_id,context_revision,context_generation,context_reason,owner_data
			FROM agent_recoveries WHERE session_id=? AND agent_id=? AND incident_id=?`, sessionID, records[0].AgentID, incidentID).Scan(
			&pending, &snapshot.checkpoint.InputAdmitted, &archive, &active, &snapshot.checkpoint.ContextOwnerID,
			&snapshot.checkpoint.ContextRevision, &snapshot.checkpoint.ContextGeneration, &snapshot.checkpoint.ContextReason, &snapshot.checkpoint.OwnerData,
		); err != nil {
			return snapshot, err
		}
		for _, value := range []struct {
			raw    []byte
			target any
		}{{pending, &snapshot.checkpoint.PendingInput}, {archive, &snapshot.checkpoint.Archive}, {active, &snapshot.checkpoint.ActiveContext}} {
			if err := json.Unmarshal(value.raw, value.target); err != nil {
				return snapshot, err
			}
		}
	}
	rows, err := db.QueryContext(ctx, `SELECT internal_id,message_json FROM immutable_message_identities
		WHERE session_id=? AND agent_id=? ORDER BY internal_id`, sessionID, ownerID)
	if err != nil {
		return snapshot, err
	}
	defer rows.Close()
	snapshot.identities = make(map[string]string)
	for rows.Next() {
		var id string
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			return snapshot, err
		}
		snapshot.identities[id] = string(raw)
	}
	return snapshot, rows.Err()
}

func checkSessionSupervisedProtocolJourney(t *testing.T, protocol, owner, outcome string, fixture sessionSupervisedProtocolFixture) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	partial := outcome == "complete" || outcome == "stop"
	original := "ORIGINAL-" + protocol + "-" + owner
	accepted := "ACCEPTED-" + protocol + "-" + owner
	partialID := "incomplete-" + protocol + "-" + owner
	model := "gpt-session-supervised-" + protocol + "-" + owner + "-" + outcome
	parentModel := model + "-parent"
	provider.SetModelContext(model, 1_000_000)
	provider.SetModelContext(parentModel, 1_000_000)
	var parentID string
	var requests, parentRequests atomic.Int32
	var bodiesMu sync.Mutex
	var bodies [][]byte
	var requestTimes []time.Time
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var raw json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			t.Errorf("decode local request: %v", err)
			return
		}
		var request struct{ Model string }
		if err := json.Unmarshal(raw, &request); err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", fixture.contentType)
		if owner == "child" && request.Model == parentModel {
			parentRequests.Add(1)
			if _, err := fmt.Fprint(w, fixture.reply("parent seed finished", "", "")); err != nil {
				t.Error(err)
			}
			return
		}
		if request.Model != model {
			t.Errorf("unexpected owner model %q", request.Model)
		}
		bodiesMu.Lock()
		bodies = append(bodies, append([]byte(nil), raw...))
		requestTimes = append(requestTimes, time.Now())
		bodiesMu.Unlock()
		index := requests.Add(1)
		var response string
		switch {
		case !partial:
			status := http.StatusBadRequest
			response = `{"error":{"type":"invalid_request_error","param":"tools[0]","message":"invalid tool schema"}}`
			switch outcome {
			case "auth":
				status, response = http.StatusUnauthorized, `{"error":{"type":"authentication_error","message":"invalid credential"}}`
			case "quota":
				status, response = http.StatusTooManyRequests, `{"error":{"code":"insufficient_quota","message":"billing limit"}}`
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
		case index == 1:
			response = fixture.partial(accepted, partialID)
		case owner == "child" && index == 2:
			arguments, err := json.Marshal(map[string]any{"recipient_ids": []string{parentID}, "kind": "final",
				"summary": "Supervised journey finished", "body": "The retained assignment and accepted work were continued without executing the incomplete write."})
			if err != nil {
				t.Error(err)
				return
			}
			response = fixture.reply("", "final-report-call", string(arguments))
		default:
			response = fixture.reply("journey finished", "", "")
		}
		if _, err := fmt.Fprint(w, response); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(server.Close)
	home, sessions, project := t.TempDir(), t.TempDir(), t.TempDir()
	guard := filepath.Join(project, "guard.txt")
	const guardText = "unchanged local evidence\n"
	if err := os.WriteFile(guard, []byte(guardText), 0600); err != nil {
		t.Fatal(err)
	}
	rootModel := model
	if owner == "child" {
		rootModel = parentModel
	}
	cfg := modelAttributionConfig(server.URL, rootModel)
	account := cfg.Accounts["account"]
	account.Connection.APIStandard = protocol
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
	t.Cleanup(func() { waitSessionSupervisedJourney(t, "session close", s.Close) })
	t.Cleanup(func() {
		if t.Failed() {
			waitSessionSupervisedJourney(t, "failed journey close", s.Close)
			if data, err := os.ReadFile(filepath.Join(s.SessionDir(), "session.log")); err == nil {
				t.Logf("isolated session diagnostic:\n%s", data)
			}
		}
	})
	s.SetWorkDir(project)
	s.SetYolo(true)
	var store *collaboration.Store
	if owner == "child" {
		// The journal has the workspace lifetime, independently of either session DB.
		journal := sessiondb.OpenAndMigrateAt(t.TempDir())
		t.Cleanup(func() { _ = journal.Close() })
		store, err = collaboration.Attach(journal, project, filepath.Join(home, "locks"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			waitSessionSupervisedJourney(t, "session close before journal detach", s.Close)
			_ = store.Close()
		})
		s.SetCollaboration(store, "supervised-journey-owner")
	}
	sender := &sessionSupervisedJourneySender{mockEventSender: newMockEventSender()}
	type evidence struct {
		snapshot sessionSupervisedJourneySnapshot
		mirror   sessiondb.RecoveryCheckpoint
		err      error
	}
	observed := make(chan evidence, 1)
	var once sync.Once
	sender.observe = func(typ string, data any, id string) {
		status, ok := data.(recoverytypes.Status)
		if typ != "recovery" || !ok || status.State == recoverytypes.Recovered || status.State == recoverytypes.Cancelled ||
			status.State == recoverytypes.Recovering && status.RetryAt.IsZero() {
			return
		}
		once.Do(func() {
			path, sessionID := s.dbPath, s.sessionID
			if owner == "child" {
				path, sessionID = filepath.Join(s.SessionDir(), "subagents", id+".db"), id
			}
			e := evidence{}
			e.snapshot, e.err = loadSessionSupervisedJourneySnapshot(ctx, path, sessionID, sessionID, status.ID)
			if e.err == nil && owner == "child" {
				var found bool
				e.mirror, found, e.err = sessiondb.LoadRecoveryCheckpoint(s.db, s.sessionID, id)
				if e.err == nil && !found {
					e.err = fmt.Errorf("child recovery has no parent mirror")
				}
			}
			observed <- e
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
		intent, err := s.PrepareInputAdmission(orch.ID, provider.PlainInput("Retain the exact parent of the supervised journey."), 1)
		if err != nil {
			t.Fatal(err)
		}
		if result := s.CommitInputAdmission(ctx, intent); !result.Accepted() {
			t.Fatalf("parent input rejected: %+v", result)
		}
		waitTransitionTurn(t, sender.mockEventSender, orch)
		if !s.initSubAgentRunner() {
			t.Fatal("session-owned child runner unavailable")
		}
		runner = s.SubAgentRunner()
		// The actual parent has finished. Close only its input loop so the child's
		// report cannot start unrelated peer turns; the session still owns the runner.
		s.CloseOrchestrator(orch.ID)
		waitSessionSupervisedJourney(t, "parent input-loop close", s.orchWg.Wait)
		result, err := runner.StartAgent(ctx, map[string]any{"category": CategoryCrafter, "description": original,
			"isolation": "shared", "allowed_tools": []string{"write_file", "publish_report"}})
		if err != nil {
			t.Fatal(err)
		}
		var identity struct {
			AgentID string `json:"agent_id"`
		}
		if err := json.Unmarshal([]byte(result.Content), &identity); err != nil || identity.AgentID == "" {
			t.Fatalf("actual child launch: %s err=%v", result.Content, err)
		}
		ownerID, ownerSessionID = identity.AgentID, identity.AgentID
		ownerPath = filepath.Join(s.SessionDir(), "subagents", ownerID+".db")
	} else {
		intent, err := s.PrepareInputAdmission(orch.ID, provider.PlainInput(original), 1)
		if err != nil {
			t.Fatal(err)
		}
		if result := s.CommitInputAdmission(ctx, intent); !result.Accepted() {
			t.Fatalf("actual root input rejected: %+v", result)
		}
	}
	var first evidence
	select {
	case first = <-observed:
	case <-ctx.Done():
		t.Fatalf("real owner never published recovery: requests=%d starts=%d results=%d", requests.Load(), sender.toolStarts.Load(), sender.toolResults.Load())
	}
	if first.err != nil {
		t.Fatal(first.err)
	}
	cp := first.snapshot.checkpoint
	if cp.Status.AgentID != ownerID || cp.Status.Generation != 1 || cp.Status.ID == "" || !cp.InputAdmitted ||
		cp.ContextOwnerID != ownerSessionID || cp.ContextRevision != first.snapshot.context.Revision ||
		cp.PendingInput.InternalID == "" || !strings.Contains(cp.PendingInput.Text, original) ||
		!reflect.DeepEqual(cp.ActiveContext, first.snapshot.context.Messages) {
		t.Fatalf("recovery did not durably retain its exact owner/source: %+v", cp)
	}
	for _, message := range cp.ActiveContext {
		if strings.Contains(message.ContentString(), original) && message.InternalID != cp.PendingInput.InternalID {
			t.Fatal("recovery replaced the admitted original input identity")
		}
	}
	if owner == "child" && (first.mirror.ContextOwnerID != "" || first.mirror.Status.ID != cp.Status.ID ||
		first.mirror.Status.AgentID != ownerID || first.mirror.ContextRevision != cp.ContextRevision ||
		!reflect.DeepEqual(first.mirror.PendingInput, cp.PendingInput) || !reflect.DeepEqual(first.mirror.ActiveContext, cp.ActiveContext) ||
		len(cp.OwnerData) == 0) {
		t.Fatalf("parent mirror does not preserve the exact child checkpoint: %+v", first.mirror)
	}
	if partial {
		if cp.Status.State != recoverytypes.Recovering || cp.Status.RetryAt.IsZero() ||
			!cp.Status.RetryAt.After(cp.Status.UpdatedAt) || cp.Status.RetryAt.Sub(cp.Status.UpdatedAt) > 5*time.Minute {
			t.Fatalf("missing real bounded automatic countdown: %+v", cp.Status)
		}
		assertSessionSupervisedJourneyMessages(t, cp.ActiveContext, original, accepted, partialID, 1)
		assertSessionSupervisedJourneyMessages(t, cp.Archive, original, accepted, partialID, 1)
	} else {
		if cp.Status.State != recoverytypes.NeedsAttention || !cp.Status.RetryAt.IsZero() || cp.Status.Detail == "" || cp.Status.Action == "" {
			t.Fatalf("permanent %s failure was not actionable/nonautomatic: %+v", outcome, cp.Status)
		}
		assertSessionSupervisedJourneyMessages(t, cp.ActiveContext, original, accepted, partialID, 0)
		// Longer than the ordinary first cooldown: an automatic misclassification
		// must be visible as a second HTTP request, not only a status mismatch.
		select {
		case <-time.After(1100 * time.Millisecond):
		case <-ctx.Done():
			t.Fatal("permanent-failure observation timed out")
		}
		if requests.Load() != 1 {
			t.Fatalf("permanent %s failure automatically retried: requests=%d", outcome, requests.Load())
		}
		if owner == "child" {
			waitSessionSupervisedJourney(t, "permanent child attempt park", runner.Wait)
			if len(runner.Manager().CompletedResults()) != 0 || len(s.PendingSubAgentSnapshots()) != 1 {
				t.Fatal("parked child task was falsely completed or lost")
			}
		}
	}
	if outcome != "complete" {
		if s.CancelRecoveryFor(ownerID, "foreign-incident", cp.Status.Generation) == nil ||
			s.CancelRecoveryFor(ownerID, cp.Status.ID, cp.Status.Generation+1) == nil {
			t.Fatal("stale Stop reached the real owner")
		}
		if s.CancelRecoveryFor(ownerID, cp.Status.ID, cp.Status.Generation) != nil {
			t.Fatal("exact-owner Stop rejected")
		}
	}
	wantReason := "completed"
	if outcome != "complete" {
		wantReason = "cancelled"
	}
	waiting := true
	for waiting {
		select {
		case event := <-sender.ch:
			if event.agentID != ownerID {
				continue
			}
			if event.typ == "recovery" {
				if status, ok := event.data.(recoverytypes.Status); ok && outcome == "complete" && status.State == recoverytypes.NeedsAttention {
					t.Fatalf("semantic continuation hit a production attention defect: %+v", status)
				}
			}
			if event.typ == "done" {
				data, _ := event.data.(map[string]any)
				if data["reason"] != wantReason {
					t.Fatalf("terminal owner reason=%v want=%s", data["reason"], wantReason)
				}
				waiting = false
			}
		case <-ctx.Done():
			t.Fatalf("real owner did not settle: requests=%d starts=%d results=%d", requests.Load(), sender.toolStarts.Load(), sender.toolResults.Load())
		}
	}
	if runner != nil {
		waitSessionSupervisedJourney(t, "child terminal publication", runner.Wait)
	}
	wantRequests, wantTools, wantChildren := int32(1), int32(0), int32(0)
	if owner == "child" {
		wantChildren = 1
	}
	if outcome == "complete" {
		wantRequests = 2
		if owner == "child" {
			wantRequests, wantTools = 3, 1
		}
	}
	if requests.Load() != wantRequests || sender.toolStarts.Load() != wantTools || sender.toolResults.Load() != wantTools || sender.children.Load() != wantChildren {
		t.Fatalf("effects: requests=%d/%d starts=%d/%d results=%d/%d children=%d/%d", requests.Load(), wantRequests,
			sender.toolStarts.Load(), wantTools, sender.toolResults.Load(), wantTools, sender.children.Load(), wantChildren)
	}
	bodiesMu.Lock()
	wireBodies, times := append([][]byte(nil), bodies...), append([]time.Time(nil), requestTimes...)
	bodiesMu.Unlock()
	if outcome == "complete" {
		if times[1].Before(cp.Status.RetryAt) || times[1].Sub(times[0]) < 900*time.Millisecond {
			t.Fatalf("replacement bypassed the real cooldown: requests=%v deadline=%s", times, cp.Status.RetryAt)
		}
		for _, raw := range wireBodies[1:] {
			wire := string(raw)
			if strings.Count(wire, original) != 1 || strings.Count(wire, accepted) != 1 ||
				!strings.Contains(wire, "runtime_recovery") || strings.Contains(wire, partialID) ||
				strings.Contains(wire, "partial-response") || strings.Contains(wire, "previous_response_id") {
				t.Fatalf("unsafe semantic continuation request: %s", wire)
			}
		}
	}
	terminal, err := loadSessionSupervisedJourneySnapshot(ctx, ownerPath, ownerSessionID, ownerSessionID, cp.Status.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantState := recoverytypes.Recovered
	if outcome != "complete" {
		wantState = recoverytypes.Cancelled
	}
	if terminal.checkpoint.Status.State != wantState || !terminal.checkpoint.Status.RetryAt.IsZero() ||
		terminal.checkpoint.Status.ID != cp.Status.ID || terminal.checkpoint.Status.AgentID != ownerID ||
		terminal.checkpoint.Status.Generation != cp.Status.Generation || !reflect.DeepEqual(terminal.checkpoint.PendingInput, cp.PendingInput) ||
		terminal.checkpoint.ContextRevision != terminal.context.Revision || !reflect.DeepEqual(terminal.checkpoint.ActiveContext, terminal.context.Messages) {
		t.Fatalf("terminal publication lost exact recovery/context identity: %+v", terminal.checkpoint)
	}
	wantTurnState := sessiondb.TurnCompleted
	if outcome != "complete" {
		wantTurnState = sessiondb.TurnInterrupted
	}
	if terminal.turn == nil || terminal.turn.AgentID != ownerID || terminal.turn.Generation != cp.Status.Generation || terminal.turn.State != wantTurnState {
		t.Fatalf("terminal acknowledgement preceded the exact durable turn ledger: %+v", terminal.turn)
	}
	assertSessionSupervisedJourneyIdentities(t, first.snapshot, terminal)
	acceptedCount := 0
	if partial {
		acceptedCount = 1
	}
	assertSessionSupervisedJourneyMessages(t, terminal.context.Messages, original, accepted, partialID, acceptedCount)
	assertSessionSupervisedJourneyMessages(t, terminal.checkpoint.Archive, original, accepted, partialID, acceptedCount)
	continuations := 0
	for _, message := range terminal.context.Messages {
		if message.EffectiveOrigin() == provider.OriginContinuation {
			if message.EffectiveVisibility() != provider.VisibilityContextOnly || !strings.Contains(message.ContentString(), "runtime_recovery") {
				t.Fatalf("recovery continuation became visible/new human input: %+v", message)
			}
			continuations++
		}
	}
	wantContinuations := 0
	if outcome == "complete" {
		wantContinuations = 1
	}
	if continuations != wantContinuations {
		t.Fatalf("semantic recovery continuations=%d want=%d", continuations, wantContinuations)
	}
	var transcript []provider.ChatMessage
	for _, row := range terminal.transcript {
		transcript = append(transcript, row.Msg)
	}
	assertSessionSupervisedJourneyMessages(t, transcript, original, accepted, partialID, acceptedCount)
	if outcome == "complete" {
		for _, messages := range [][]provider.ChatMessage{transcript, terminal.context.Messages, terminal.checkpoint.Archive} {
			finals := 0
			for _, message := range messages {
				if message.Role == "assistant" && message.ContentString() == "journey finished" {
					finals++
				}
			}
			if finals != 1 {
				t.Fatalf("successful final assistant boundary count=%d", finals)
			}
		}
	}
	if outcome == "complete" && owner == "child" {
		result, found := runner.Manager().GetCompleted(agent.AgentID(ownerID))
		if !found || result.Status != agent.SubAgentCompleted || result.ReportID == "" || result.ReportRevision != 1 {
			t.Fatalf("actual child did not finish with its real final report: %+v", result)
		}
		artifact, err := store.Artifact(ctx, result.ReportID, result.ReportRevision)
		if err != nil || artifact.AuthorID != ownerID || artifact.Kind != collaboration.ReportFinal || artifact.RunID == "" {
			t.Fatalf("final report has wrong author/run provenance: %+v err=%v", artifact, err)
		}
		run, err := store.Run(ctx, artifact.RunID)
		if err != nil || run.AgentID != ownerID || run.State != collaboration.RunSucceeded {
			t.Fatalf("real final report run did not durably settle: %+v err=%v", run, err)
		}
		page, err := store.Page(ctx, collaboration.PageQuery{RecipientID: parentID, Kind: collaboration.EventReportPublished})
		if err != nil || len(page.Events) != 1 || page.Events[0].SenderID != ownerID || page.Events[0].RunID != artifact.RunID {
			t.Fatalf("final report did not address the exact parent: %+v err=%v", page, err)
		}
		reportResults := 0
		for _, message := range terminal.context.Messages {
			if message.Role == "tool" && !message.ToolError {
				var report collaboration.PublishReportResult
				if json.Unmarshal([]byte(message.ContentString()), &report) == nil && report.Artifact.ID == artifact.ID && report.Artifact.Revision == artifact.Revision {
					reportResults++
				}
			}
		}
		if reportResults != 1 {
			t.Fatalf("durable actual publish_report result count=%d", reportResults)
		}
	}
	rootPath := s.dbPath
	beforeChildren := s.CompletedSubAgentSnapshots()
	waitSessionSupervisedJourney(t, "terminal session Close", s.Close)
	resumed, err := ResumeSessionWithDir(rootPath, cfg, home, sessions)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { waitSessionSupervisedJourney(t, "resumed session close", resumed.Close) })
	resumed.SetWorkDir(project)
	if store != nil {
		resumed.SetCollaboration(store, "supervised-journey-resumed")
	}
	if _, err := resumed.NewOrchestrator(sender); err != nil {
		t.Fatal(err)
	}
	// Cross the product's idle collaboration poll, not only the return from Resume.
	select {
	case <-time.After(600 * time.Millisecond):
	case <-ctx.Done():
		t.Fatal("passive resume observation timed out")
	}
	after, err := loadSessionSupervisedJourneySnapshot(ctx, ownerPath, ownerSessionID, ownerSessionID, cp.Status.ID)
	if err != nil || !reflect.DeepEqual(terminal, after) || !reflect.DeepEqual(beforeChildren, resumed.CompletedSubAgentSnapshots()) {
		afterChildren := resumed.CompletedSubAgentSnapshots()
		if len(beforeChildren) == 1 && len(afterChildren) == 1 {
			t.Logf("child resume: messages=%v compactions=%v recovery=%v model=%v result-before=%+v result-after=%+v", reflect.DeepEqual(beforeChildren[0].Messages, afterChildren[0].Messages), reflect.DeepEqual(beforeChildren[0].Compactions, afterChildren[0].Compactions), reflect.DeepEqual(beforeChildren[0].Recovery, afterChildren[0].Recovery), beforeChildren[0].Model == afterChildren[0].Model, beforeChildren[0].Result, afterChildren[0].Result)
			before, after := reflect.ValueOf(beforeChildren[0].Result), reflect.ValueOf(afterChildren[0].Result)
			for i := 0; i < before.NumField(); i++ {
				if !reflect.DeepEqual(before.Field(i).Interface(), after.Field(i).Interface()) {
					t.Logf("child result %s: before=%#v after=%#v", before.Type().Field(i).Name, before.Field(i).Interface(), after.Field(i).Interface())
				}
			}
		}
		t.Fatalf("Close/passive Resume changed evidence: checkpoint=%v context=%v transcript=%v identities=%v ledger=%v children=%v error=%v", reflect.DeepEqual(terminal.checkpoint, after.checkpoint), reflect.DeepEqual(terminal.context, after.context), reflect.DeepEqual(terminal.transcript, after.transcript), reflect.DeepEqual(terminal.identities, after.identities), reflect.DeepEqual(terminal.turn, after.turn), reflect.DeepEqual(beforeChildren, resumed.CompletedSubAgentSnapshots()), err)
	}
	if requests.Load() != wantRequests || parentRequests.Load() != int32(len(beforeChildren)) ||
		sender.toolStarts.Load() != wantTools || sender.toolResults.Load() != wantTools || sender.children.Load() != wantChildren ||
		len(resumed.PendingSubAgentSnapshots()) != 0 || resumed.SubAgentRunner() != nil && resumed.SubAgentRunner().Running() {
		t.Fatalf("passive Resume executed work: requests=%d parent=%d starts=%d results=%d children=%d", requests.Load(), parentRequests.Load(), sender.toolStarts.Load(), sender.toolResults.Load(), sender.children.Load())
	}
	contents, err := os.ReadFile(guard)
	entries, listErr := os.ReadDir(project)
	if err != nil || listErr != nil || !bytes.Equal(contents, []byte(guardText)) || len(entries) != 1 || entries[0].Name() != "guard.txt" {
		t.Fatalf("incomplete call or passive Resume mutated the project: read=%v list=%v entries=%v content=%q", err, listErr, entries, contents)
	}
	t.Logf("%s/%s/%s: owner requests=%d parent seed=%d tool starts/results=%d/%d children=%d; durable incident %s generation=%d; passive replay=0",
		protocol, owner, outcome, requests.Load(), parentRequests.Load(), sender.toolStarts.Load(), sender.toolResults.Load(), sender.children.Load(), cp.Status.ID, cp.Status.Generation)
}

func waitSessionSupervisedJourney(t *testing.T, name string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { fn(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("%s timed out", name)
	}
}

func assertSessionSupervisedJourneyMessages(t *testing.T, messages []provider.ChatMessage, original, accepted, partialID string, wantAccepted int) {
	t.Helper()
	originals, partials := 0, 0
	ids := make(map[string]bool)
	for _, message := range messages {
		if message.Role == "system" {
			continue
		}
		if message.InternalID != "" {
			if ids[message.InternalID] {
				t.Fatalf("duplicate immutable message identity %q", message.InternalID)
			}
			ids[message.InternalID] = true
		}
		if strings.Contains(message.ContentString(), original) {
			originals++
		}
		if message.Role == "assistant" && message.ContentString() == accepted {
			partials++
		}
		encoded, err := json.Marshal(message)
		if err != nil || strings.Contains(string(encoded), partialID) || strings.Contains(string(encoded), "partial-response") {
			t.Fatalf("incomplete call/reference became canonical evidence: %+v err=%v", message, err)
		}
		for _, call := range message.ToolCalls {
			if call.Function.Name != "publish_report" {
				t.Fatalf("incomplete write became an executable call: %+v", call)
			}
		}
	}
	if originals != 1 || partials != wantAccepted {
		t.Fatalf("lost/duplicated owner work: original=%d accepted=%d/%d", originals, partials, wantAccepted)
	}
}

func assertSessionSupervisedJourneyIdentities(t *testing.T, first, terminal sessionSupervisedJourneySnapshot) {
	t.Helper()
	for id, raw := range first.identities {
		if terminal.identities[id] != raw {
			t.Fatalf("terminal publication rewrote indexed immutable identity %s", id)
		}
	}
	for _, message := range terminal.context.Messages {
		if message.Role != "system" && terminal.identities[message.InternalID] == "" {
			t.Fatalf("context message %q bypassed the immutable identity registry", message.InternalID)
		}
	}
	byID := make(map[string]sessiondb.MessageWithMeta)
	for _, row := range terminal.transcript {
		if row.Msg.InternalID == "" || byID[row.Msg.InternalID].Msg.InternalID != "" || terminal.identities[row.Msg.InternalID] == "" {
			t.Fatalf("transcript has a missing/duplicate/unregistered identity %q", row.Msg.InternalID)
		}
		byID[row.Msg.InternalID] = row
	}
	for _, row := range first.transcript {
		if !reflect.DeepEqual(row, byID[row.Msg.InternalID]) {
			t.Fatalf("terminal publication restamped/replaced transcript %s", row.Msg.InternalID)
		}
	}
}

type sessionSupervisedProtocolFixture struct {
	contentType string
	partial     func(accepted, partialID string) string
	reply       func(text, callID, arguments string) string
}

func sessionSupervisedProtocolFixtures() map[string]sessionSupervisedProtocolFixture {
	responses := sessionSupervisedProtocolFixture{"text/event-stream", func(text, id string) string {
		return fmt.Sprintf("data: {\"type\":\"response.created\",\"response\":{\"id\":\"partial-response\",\"status\":\"in_progress\",\"store\":true,\"output\":[]}}\n\ndata: {\"type\":\"response.output_text.delta\",\"delta\":%q}\n\ndata: {\"type\":\"response.output_item.added\",\"output_index\":1,\"item\":{\"id\":%q,\"type\":\"function_call\",\"call_id\":%q,\"name\":\"write_file\",\"arguments\":\"\"}}\n\ndata: {\"type\":\"response.function_call_arguments.delta\",\"output_index\":1,\"delta\":\"{\\\"path\\\":\\\"guard.txt\\\",\\\"content\\\":\"}\n\n", text, id, id)
	}, func(text, id, args string) string {
		output := fmt.Sprintf("{\"type\":\"message\",\"id\":\"final-message\",\"role\":\"assistant\",\"status\":\"completed\",\"content\":[{\"type\":\"output_text\",\"text\":%q}]}", text)
		if id != "" {
			output = fmt.Sprintf("{\"type\":\"function_call\",\"call_id\":%q,\"name\":\"publish_report\",\"arguments\":%q,\"status\":\"completed\"}", id, args)
		}
		return fmt.Sprintf("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"final-response\",\"status\":\"completed\",\"store\":false,\"output\":[%s]}}\n\n", output)
	}}
	return map[string]sessionSupervisedProtocolFixture{
		"openai": {"text/event-stream", func(text, id string) string {
			return fmt.Sprintf("data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\ndata: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":%q,\"type\":\"function\",\"function\":{\"name\":\"write_file\",\"arguments\":\"{\\\"path\\\":\\\"guard.txt\\\",\\\"content\\\":\"}}]}}]}\n\n", text, id)
		}, func(text, id, args string) string {
			if id != "" {
				return fmt.Sprintf("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":%q,\"type\":\"function\",\"function\":{\"name\":\"publish_report\",\"arguments\":%q}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n", id, args)
			}
			return fmt.Sprintf("data: {\"choices\":[{\"delta\":{\"content\":%q},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", text)
		}},
		"openai-responses": responses,
		"codex":            responses,
		"anthropic": {"text/event-stream", func(text, id string) string {
			return fmt.Sprintf("data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":%q}}\n\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"tool_use\",\"id\":%q,\"name\":\"write_file\",\"input\":{}}}\n\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"path\\\":\\\"guard.txt\\\",\\\"content\\\":\"}}\n\n", text, id)
		}, func(text, id, args string) string {
			if id != "" {
				return fmt.Sprintf("data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":%q,\"name\":\"publish_report\",\"input\":{}}}\n\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":%q}}\n\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n", id, args)
			}
			return fmt.Sprintf("data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":%q}}\n\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n", text)
		}},
		"ollama": {"application/x-ndjson", func(text, id string) string {
			return fmt.Sprintf("{\"model\":\"m\",\"message\":{\"role\":\"assistant\",\"content\":%q}}\n{\"model\":\"m\",\"message\":{\"role\":\"assistant\",\"tool_calls\":[{\"id\":%q,\"function\":{\"name\":\"write_file\",\"arguments\":\"{\\\"path\\\":\\\"guard.txt\\\",\\\"content\\\":\"}}]}}\n", text, id)
		}, func(text, id, args string) string {
			if id != "" {
				return fmt.Sprintf("{\"model\":\"m\",\"message\":{\"role\":\"assistant\",\"tool_calls\":[{\"id\":%q,\"function\":{\"name\":\"publish_report\",\"arguments\":%s}}]},\"done\":true,\"done_reason\":\"stop\"}\n", id, args)
			}
			return fmt.Sprintf("{\"model\":\"m\",\"message\":{\"role\":\"assistant\",\"content\":%q},\"done\":true,\"done_reason\":\"stop\"}\n", text)
		}},
	}
}
