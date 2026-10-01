package session

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"crude/logger"
	"crude/provider"
	"crude/sessiondb"
)

type adaptiveCompactionProbe struct {
	capacity      int
	model         string
	calls         atomic.Int32
	sawStart      atomic.Bool
	sawEnd        atomic.Bool
	sawCorrection atomic.Bool
}

func (probe *adaptiveCompactionProbe) readRequest(t *testing.T, request *http.Request) (provider.ChatRequest, int32) {
	t.Helper()
	var body provider.ChatRequest
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		t.Errorf("decode compaction request: %v", err)
	}
	call := probe.calls.Add(1)
	input := provider.EstimateMessagesTotal(body.Messages, body.Model)
	output := max(body.MaxTokens, body.MaxCompletionTokens)
	if body.Model != probe.model || output <= 0 || input+output > probe.capacity {
		t.Errorf("request %d: model=%q input=%d output=%d capacity=%d", call, body.Model, input, output, probe.capacity)
	}
	if len(body.Tools) != 0 || body.ToolChoice != "none" {
		t.Errorf("request %d is not no-tools extraction: tools=%d choice=%v", call, len(body.Tools), body.ToolChoice)
	}
	for _, message := range body.Messages {
		text := message.ContentString()
		if strings.Contains(text, "adaptive-source-start") {
			probe.sawStart.Store(true)
		}
		if strings.Contains(text, "adaptive-source-end") {
			probe.sawEnd.Store(true)
		}
		if strings.Contains(text, "Keep parser.go unchanged; preserve the no-delete constraint and failing test evidence.") {
			probe.sawCorrection.Store(true)
		}
	}
	return body, call
}

func writeAdaptiveCompactionSSE(t *testing.T, writer http.ResponseWriter, text string) {
	t.Helper()
	encoded, err := json.Marshal(text)
	if err != nil {
		t.Errorf("encode SSE content: %v", err)
		return
	}
	writer.Header().Set("Content-Type", "text/event-stream")
	if _, err := fmt.Fprintf(writer, "data: {\"choices\":[{\"delta\":{\"content\":%s},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", encoded); err != nil {
		t.Errorf("write compaction SSE: %v", err)
	}
}

func adaptiveCompactionMessages(t *testing.T, role string) []provider.ChatMessage {
	t.Helper()
	huge := provider.NewTextMessage(role, "adaptive-source-start\n"+strings.Repeat("old evidence ", 120_000)+"\nadaptive-source-end")
	huge.InternalID = "large-evidence"
	history := []provider.ChatMessage{provider.NewTextMessage("user", "Investigate parser.go without deleting source files.")}
	if role == "tool" {
		call := provider.NewTextMessage("assistant", "Collecting the parser test evidence.")
		call.ToolCalls = []provider.ToolCall{{
			ID: "evidence-call", Type: "function",
			Function: provider.FunctionCall{Name: "read_file", Arguments: `{"path":"parser.go"}`},
		}}
		huge.ToolCallID = "evidence-call"
		history = append(history, call)
	}
	history = append(history, huge, provider.NewTextMessage("user", "Keep parser.go unchanged; preserve the no-delete constraint and failing test evidence."))
	if tokens := provider.EstimateMessagesTotal(history, "adaptive-main-model"); tokens < 350_000 {
		t.Fatalf("source is not oversized enough: %d tokens", tokens)
	}
	return history
}

func newAdaptiveCompactionSession(t *testing.T, serverURL string, capacity int, history []provider.ChatMessage) (*Session, string) {
	t.Helper()
	const sessionID = "adaptive-compact"
	dbPath := filepath.Join(t.TempDir(), "session.db")
	db, err := sessiondb.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sessiondb.CreateSessionRow(db, sessionID); err != nil {
		t.Fatal(err)
	}
	if err := sessiondb.SaveContextState(db, sessionID, sessionID, sessiondb.ContextState{
		AgentID: sessionID, Generation: 7, Reason: "turn_complete", Messages: history,
	}); err != nil {
		t.Fatal(err)
	}
	log, err := logger.New(logger.Options{HomeDir: t.TempDir(), SessionID: sessionID})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = log.Close() })
	cfg := testConfig()
	profile := cfg.Agents.Profiles[cfg.Agents.ActiveProfile]
	profile.Categories[CategoryDefault] = CategoryConfig{
		AccountID: "account", Model: "adaptive-main-model", ModelContextWindow: 1_000_000,
	}
	profile.Categories[CategoryAeron] = CategoryConfig{
		AccountID: "account", Model: "adaptive-aeron-model", ModelContextWindow: capacity, ModelMaxOutput: 2_048,
	}
	cfg.Agents.Profiles[cfg.Agents.ActiveProfile] = profile
	template := cfg.Providers[cfg.ActiveProvider]
	template.ProviderURL = serverURL
	cfg.Providers[cfg.ActiveProvider] = template
	main, err := cfg.providerForCategory(cfg.GetActiveDefaultCategory())
	if err != nil {
		t.Fatal(err)
	}
	aeron, err := cfg.providerForCategory(cfg.GetCategoryConfig(CategoryAeron))
	if err != nil {
		t.Fatal(err)
	}
	if main.ContextWindow(main.Model) != 1_000_000 || aeron.ContextWindow(aeron.Model) != capacity {
		t.Fatalf("category overrides not bound: main=%d Aeron=%d", main.ContextWindow(main.Model), aeron.ContextWindow(aeron.Model))
	}
	return &Session{
		logger: log, db: db, cfg: cfg, sessionID: sessionID,
		msgs: append([]provider.ChatMessage(nil), history...), activeContext: append([]provider.ChatMessage(nil), history...),
		contextGeneration: 7, contextReason: "turn_complete",
	}, dbPath
}

func adaptiveMessageBytes(t *testing.T, messages []provider.ChatMessage) []byte {
	t.Helper()
	payload, err := json.Marshal(messages)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func TestSessionAdaptiveCompactionInstallsCheckpointAndResumesPassively(t *testing.T) {
	for _, fixture := range []struct {
		capacity int
		role     string
	}{{capacity: 64_000, role: "assistant"}, {capacity: 128_000, role: "tool"}} {
		t.Run(fmt.Sprintf("%d_%s", fixture.capacity, fixture.role), func(t *testing.T) {
			probe := &adaptiveCompactionProbe{capacity: fixture.capacity, model: "adaptive-aeron-model"}
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				_, call := probe.readRequest(t, request)
				writeAdaptiveCompactionSSE(t, writer, fmt.Sprintf("- extraction %d: parser.go unchanged; no-delete constraint; failing test evidence retained", call))
			}))
			defer server.Close()
			history := adaptiveCompactionMessages(t, fixture.role)
			original := adaptiveMessageBytes(t, history)
			session, dbPath := newAdaptiveCompactionSession(t, server.URL, fixture.capacity, history)
			result := session.HandleCompact()
			if result.Error != "" || result.Cancelled || result.OperationID == "" {
				t.Fatalf("adaptive compaction failed: %+v", result)
			}
			if probe.calls.Load() <= 4 || !probe.sawStart.Load() || !probe.sawEnd.Load() || !probe.sawCorrection.Load() {
				t.Fatalf("source not extracted adaptively: calls=%d start=%v end=%v correction=%v", probe.calls.Load(), probe.sawStart.Load(), probe.sawEnd.Load(), probe.sawCorrection.Load())
			}
			if !bytes.Equal(adaptiveMessageBytes(t, session.msgs), original) || !bytes.Equal(adaptiveMessageBytes(t, history), original) {
				t.Fatal("compaction changed the original full message archive")
			}
			if session.contextGeneration != 8 || session.contextReason != "manual_compaction" || len(session.activeContext) != 3 {
				t.Fatalf("checkpoint not installed: generation=%d reason=%q messages=%d", session.contextGeneration, session.contextReason, len(session.activeContext))
			}
			checkpoint := session.activeContext[0]
			var document struct {
				XMLName      xml.Name `xml:"context_checkpoint"`
				Version      string   `xml:"version,attr"`
				Role         string   `xml:"role,attr"`
				Intent       string   `xml:"intent"`
				Work         string   `xml:"work"`
				State        string   `xml:"state"`
				Continuation string   `xml:"continuation"`
			}
			if err := xml.Unmarshal([]byte(checkpoint.ContentString()), &document); err != nil {
				t.Fatalf("invalid checkpoint XML: %v", err)
			}
			if checkpoint.Origin != provider.OriginCheckpoint || checkpoint.Visibility != provider.VisibilityContextOnly || document.Version != "3" || document.Role != "root" {
				t.Fatalf("invalid checkpoint contract: origin=%q visibility=%q version=%q role=%q", checkpoint.Origin, checkpoint.Visibility, document.Version, document.Role)
			}
			for name, section := range map[string]string{"intent": document.Intent, "work": document.Work, "state": document.State, "continuation": document.Continuation} {
				if !strings.Contains(section, "no-delete constraint") {
					t.Errorf("%s stage not installed: %q", name, section)
				}
			}
			if !bytes.Equal(adaptiveMessageBytes(t, session.activeContext[1:]), adaptiveMessageBytes(t, []provider.ChatMessage{history[0], history[len(history)-1]})) {
				t.Fatal("genuine human request and final correction were not retained verbatim")
			}
			var archived []byte
			if err := session.db.QueryRow(`SELECT messages_json FROM agent_context_generations WHERE session_id = ? AND agent_id = ? AND generation = ?`, session.sessionID, session.sessionID, 7).Scan(&archived); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(archived, original) {
				t.Fatal("previous generation does not contain the exact source context")
			}
			records, err := sessiondb.ListCompactionRecords(session.db, session.sessionID, session.sessionID)
			if err != nil || len(records) != 1 {
				t.Fatalf("load compaction records: count=%d err=%v", len(records), err)
			}
			record := records[0]
			if record.OperationID != result.OperationID || record.State != sessiondb.CompactionCompleted || record.ModelID != probe.model || record.CompletedStages != 4 || record.StageCount != 4 || record.BeforeTokens < 350_000 || record.AfterTokens >= record.BeforeTokens || record.EndedAt.IsZero() {
				t.Fatalf("completed record is incorrect: %+v", record)
			}
			callsBeforeResume := probe.calls.Load()
			if err := session.db.Close(); err != nil {
				t.Fatal(err)
			}
			resumedDB, err := sessiondb.OpenAndMigrate(dbPath)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = resumedDB.Close() }()
			if err := sessiondb.InterruptRunningCompactions(resumedDB, session.sessionID); err != nil {
				t.Fatal(err)
			}
			resumed, found, err := sessiondb.LoadContextState(resumedDB, session.sessionID, session.sessionID)
			if err != nil || !found || resumed.Generation != 8 || resumed.Reason != "manual_compaction" || !bytes.Equal(adaptiveMessageBytes(t, resumed.Messages), adaptiveMessageBytes(t, session.activeContext)) {
				t.Fatalf("passive resume restored wrong context: found=%v generation=%d reason=%q err=%v", found, resumed.Generation, resumed.Reason, err)
			}
			resumedRecords, err := sessiondb.ListCompactionRecords(resumedDB, session.sessionID, session.sessionID)
			if err != nil || len(resumedRecords) != 1 || resumedRecords[0].State != sessiondb.CompactionCompleted || probe.calls.Load() != callsBeforeResume {
				t.Fatalf("passive resume changed terminal operation or called provider: records=%+v calls=%d err=%v", resumedRecords, probe.calls.Load(), err)
			}
		})
	}
}

func TestSessionCompactionLearnsOutputLimitAcrossNewSessions(t *testing.T) {
	catalog, err := provider.OpenModelCatalog(filepath.Join(t.TempDir(), "model-catalog.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	provider.UseModelCatalog(catalog)
	t.Cleanup(func() { _ = catalog.Close() })
	var outputs []int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body provider.ChatRequest
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		output := max(body.MaxTokens, body.MaxCompletionTokens)
		outputs = append(outputs, output)
		if output > 393216 {
			writer.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprintf(writer, `{"error":{"type":"invalid_request_error","message":"max_tokens (current value: %d) must be between 0 and 393216"}}`, output)
			return
		}
		writeAdaptiveCompactionSSE(t, writer, "- original evidence, correction and ownership preserved")
	}))
	defer server.Close()
	for run := 0; run < 2; run++ {
		history := adaptiveCompactionMessages(t, "assistant")
		original := adaptiveMessageBytes(t, history)
		s, _ := newAdaptiveCompactionSession(t, server.URL, 1_050_000, history)
		profile := s.cfg.Agents.Profiles[s.cfg.Agents.ActiveProfile]
		aeron := profile.Categories[CategoryAeron]
		aeron.ModelMaxOutput = 600000
		profile.Categories[CategoryAeron] = aeron
		s.cfg.Agents.Profiles[s.cfg.Agents.ActiveProfile] = profile
		outputs = nil
		result := s.HandleCompact()
		if result.Error != "" || result.Cancelled || !bytes.Equal(adaptiveMessageBytes(t, s.msgs), original) {
			t.Fatalf("run %d: compaction failed or rewrote source: %+v", run, result)
		}
		if run == 0 {
			if len(outputs) < 5 || outputs[0] <= 393216 {
				t.Fatalf("first session did not exercise provider rejection: %v", outputs)
			}
		} else if len(outputs) != 4 {
			t.Fatalf("new session did not inherit its Aeron profile's learned cap: %v", outputs)
		}
		for _, cap := range outputs[1:] {
			if cap > 393216 {
				t.Fatalf("session %d ignored learned cap: %v", run, outputs)
			}
		}
	}
}

func TestSessionAdaptiveCompactionLaterChunkFailurePreservesDurableSource(t *testing.T) {
	for _, failure := range []string{"permanent_401", "empty_after_retries"} {
		t.Run(failure, func(t *testing.T) {
			probe := &adaptiveCompactionProbe{capacity: 64_000, model: "adaptive-aeron-model"}
			var session *Session
			var original []byte
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				body, call := probe.readRequest(t, request)
				if call == 1 {
					source, found, err := sessiondb.LoadContextState(session.db, session.sessionID, session.sessionID)
					if err != nil || !found || source.Generation != 7 || source.Reason != "compaction_source" || !bytes.Equal(adaptiveMessageBytes(t, source.Messages), original) {
						t.Errorf("exact source was not durable before extraction: found=%v generation=%d reason=%q err=%v", found, source.Generation, source.Reason, err)
					}
				}
				if call >= 3 {
					if !strings.Contains(body.Messages[len(body.Messages)-1].ContentString(), "source chunk 3/") {
						t.Errorf("failure did not occur on the later source chunk: call=%d", call)
					}
					if failure == "permanent_401" {
						http.Error(writer, `{"error":{"message":"invalid Aeron credentials","type":"authentication_error"}}`, http.StatusUnauthorized)
						return
					}
					writeAdaptiveCompactionSSE(t, writer, "")
					return
				}
				writeAdaptiveCompactionSSE(t, writer, "- partial evidence retained")
			}))
			defer server.Close()
			history := adaptiveCompactionMessages(t, "tool")
			original = adaptiveMessageBytes(t, history)
			session, _ = newAdaptiveCompactionSession(t, server.URL, probe.capacity, history)
			prepared, early := session.PrepareCompaction(session.sessionID)
			if early.Error != "" || prepared.Run == nil || prepared.ModelID != probe.model {
				t.Fatalf("prepare adaptive compaction: prepared=%+v early=%+v", prepared, early)
			}
			result := prepared.Run()
			if result.Error == "" || result.Cancelled || result.OperationID != prepared.OperationID || session.compactionActive(session.sessionID) {
				t.Fatalf("failed extraction did not terminate correctly: %+v", result)
			}
			expectedCalls, expectedRetries := int32(3), 0
			if failure == "empty_after_retries" {
				expectedCalls, expectedRetries = 6, 3
			}
			if probe.calls.Load() != expectedCalls {
				t.Fatalf("retry bound changed: calls=%d want=%d", probe.calls.Load(), expectedCalls)
			}
			if session.contextGeneration != 7 || !bytes.Equal(adaptiveMessageBytes(t, session.activeContext), original) || !bytes.Equal(adaptiveMessageBytes(t, session.msgs), original) {
				t.Fatal("later-chunk failure replaced or mutated the old active context or archive")
			}
			source, found, err := sessiondb.LoadContextState(session.db, session.sessionID, session.sessionID)
			if err != nil || !found || source.Generation != 7 || source.Reason != "compaction_source" || !bytes.Equal(adaptiveMessageBytes(t, source.Messages), original) {
				t.Fatalf("failure lost durable source: found=%v generation=%d reason=%q err=%v", found, source.Generation, source.Reason, err)
			}
			records, err := sessiondb.ListCompactionRecords(session.db, session.sessionID, session.sessionID)
			if err != nil || len(records) != 1 {
				t.Fatalf("load failed compaction record: count=%d err=%v", len(records), err)
			}
			record := records[0]
			if record.State != sessiondb.CompactionFailed || record.ModelID != probe.model || record.CompletedStages != 0 || record.RetryAttempt != expectedRetries || record.Error == "" || record.EndedAt.IsZero() {
				t.Fatalf("failure incorrectly persisted terminal lifecycle: %+v", record)
			}
		})
	}
}
