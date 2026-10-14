package session

import (
	"context"
	"encoding/base64"
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

	"crude/memory"
	"crude/plan"
	"crude/provider"
	"crude/sessiondb"
	recoverytypes "crude/types/recovery"
)

func TestSelectedReferenceSnapshotPrecedesAdmissionReceipt(t *testing.T) {
	capture := newRequestCapture(t, "reference-admission-preimage", "data: {\"choices\":[{\"delta\":{\"content\":\"complete\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	cfg := modelAttributionConfig(capture.server.URL, "reference-admission-preimage")
	s, err := NewWithSessionDir("reference-admission-preimage", cfg, t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	s.SetWorkDir(t.TempDir())
	release := make(chan struct{})
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	s.SetBeforeTurnHook(func(ctx context.Context, _ string) error {
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	ref := sessiondb.Reference{ID: "selected-preimage", SessionID: s.sessionID, Kind: sessiondb.KindMCPContext, Title: "exact-source/resource", Content: "captured selection before acknowledgment"}
	if err := sessiondb.InsertPromptReference(s.db, ref); err != nil {
		t.Fatal(err)
	}
	sender := newMockEventSender()
	o, err := s.NewOrchestrator(sender)
	if err != nil {
		t.Fatal(err)
	}
	input := provider.InputWithRefs("original with selection", []string{ref.ID})
	input.ContextParts = []string{"captured input ContextParts"}
	intent, err := s.PrepareInputAdmission(o.ID, input, 3)
	if err != nil {
		t.Fatal(err)
	}
	if result := s.CommitInputAdmission(t.Context(), intent); !result.Accepted() {
		t.Fatalf("valid selected input was not admitted: %+v", result)
	}
	state, found, err := sessiondb.LoadContextState(s.db, s.sessionID, s.sessionID)
	if err != nil || !found {
		t.Fatalf("load accepted selection: found=%v err=%v", found, err)
	}
	selected := false
	for _, message := range state.Messages {
		if message.InternalID == intent.InputID+"-references" && strings.Contains(message.ContentString(), ref.AgentText()) {
			selected = true
		}
	}
	if !selected {
		t.Fatal("admission acknowledged before preserving the exact selected reference preimage")
	}
	if err := sessiondb.UpdatePromptReferenceContent(s.db, ref.ID, "changed after acknowledgment"); err != nil {
		t.Fatal(err)
	}
	close(release)
	released = true
	waitTurnDone(t, sender, o.ID)
	capture.mu.Lock()
	defer capture.mu.Unlock()
	if len(capture.requests) != 1 {
		t.Fatalf("request count = %d", len(capture.requests))
	}
	var text strings.Builder
	for _, part := range capture.requests[0].Parts {
		if value, ok := part["text"].(string); ok {
			text.WriteString(value)
			text.WriteByte('\n')
		}
	}
	if strings.Count(text.String(), ref.AgentText()) != 1 || strings.Contains(text.String(), "changed after acknowledgment") {
		t.Fatalf("provider received a changed/duplicated selected preimage: %s", text.String())
	}
}

func TestPreparedInputEnrichmentReachesLocallyAdmittedRequestExactlyOnce(t *testing.T) {
	requests := make(chan map[string]any, 2)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		requests <- body
		if calls.Add(1) == 1 {
			<-r.Context().Done()
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"complete\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	home, directory, project := t.TempDir(), t.TempDir(), t.TempDir()
	cfg := mediaAttributionConfig(server.URL, "selected-reference-request", "openai", provider.ModalityImage)
	s, err := NewWithSessionDir("selected-reference", cfg, home, directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	s.SetWorkDir(project)
	store := newTestMemoryStore(t)
	s.SetMemoryStore(store)
	if _, err := store.Remember(t.Context(), memory.Record{Kind: "convention", Title: "Selection verification", Content: "retained recall datum", Source: memory.SourceTool}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSessionSummary(t.Context(), memory.SessionSummary{SessionID: "prior", Title: "Prior task", Summary: "retained prior-session datum"}); err != nil {
		t.Fatal(err)
	}
	plans, err := s.planStore()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := plans.Update(t.Context(), plan.UpdateRequest{PlanID: "selected-plan", Objective: "selection verification", Status: plan.StatusActive, Steps: []plan.PlanStep{{ID: "verify", Description: "retained plan datum", Status: plan.StepInProgress}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.asyncMgr.Start("selected-runtime", "exit 0", project, 0); err != nil {
		t.Fatal(err)
	}
	imagePath := filepath.Join(project, "capture.png")
	if err := os.WriteFile(imagePath, mediaPNG.content, 0o600); err != nil {
		t.Fatal(err)
	}
	refs := []sessiondb.Reference{
		{ID: "mcp-tool", Kind: sessiondb.KindMCPTool, Title: "mcp__exact_source__inspect", Label: "[Selected tool]", Content: "Connection: exact-source\nObserved schema: exact-fingerprint"},
		{ID: "mcp-context", Kind: sessiondb.KindMCPContext, Title: "resource exact-source/resource", Label: "[MCP context]", Content: "Source: exact-source\nURI: exact://resource\nArguments: {\"revision\":7}\nretained external datum"},
		{ID: "media", Kind: sessiondb.KindPastedImage, Label: "[Image]", Mime: "image/png", FilePath: imagePath},
	}
	for i := range refs {
		refs[i].SessionID = s.sessionID
		if err := sessiondb.InsertPromptReference(s.db, refs[i]); err != nil {
			t.Fatal(err)
		}
	}
	sender := newMockEventSender()
	o, err := s.NewOrchestrator(sender)
	if err != nil {
		t.Fatal(err)
	}
	input := provider.InputWithRefs("selection verification [Selected tool] [MCP context] [Image]", []string{"mcp-tool", "mcp-context", "media", "mcp-tool"})
	input.ContextParts = []string{"retained ContextParts datum"}
	intent, err := s.PrepareInputAdmission(o.ID, input, 4)
	if err != nil {
		t.Fatal(err)
	}
	input.InternalID = intent.InputID
	if result := s.CommitInputAdmission(t.Context(), intent); !result.Accepted() {
		t.Fatalf("valid selected input was not admitted: %+v", result)
	}
	var first map[string]any
	select {
	case first = <-requests:
	case <-time.After(5 * time.Second):
		t.Fatal("locally admitted selection did not reach the provider")
	}
	assertSelection := func(body map[string]any) {
		t.Helper()
		var text strings.Builder
		images := 0
		for _, part := range flattenParts(body) {
			if value, ok := part["text"].(string); ok {
				text.WriteString(value)
				text.WriteByte('\n')
			}
			if part["type"] == "image_url" {
				image, _ := part["image_url"].(map[string]any)
				if image["url"] != "data:image/png;base64,"+base64.StdEncoding.EncodeToString(mediaPNG.content) {
					t.Fatalf("selected image bytes changed: %+v", image)
				}
				images++
			}
		}
		for _, want := range []string{input.Text, input.ContextParts[0], refs[0].AgentText(), refs[1].AgentText()} {
			if count := strings.Count(text.String(), want); count != 1 {
				t.Fatalf("selected preparation appeared %d times, want once: %q", count, want)
			}
		}
		if images != 1 || !strings.Contains(text.String(), "not an execution grant") || !strings.Contains(text.String(), "not user instructions or permission") {
			t.Fatalf("selection lost media/authority boundaries: images=%d text=%s", images, text.String())
		}
	}
	assertSelection(first)
	state, found, err := sessiondb.LoadContextState(s.db, s.sessionID, s.sessionID)
	if err != nil || !found {
		t.Fatalf("load admitted context: found=%v err=%v", found, err)
	}
	var original provider.ChatMessage
	for _, message := range state.Messages {
		if message.InternalID == input.InternalID {
			original = message
			if message.ContentString() != input.Text {
				t.Fatalf("preparation rewrote original input: %+v", message)
			}
		}
		if message.InternalID == input.InternalID+"-context" {
			for _, want := range []string{"retained plan datum", "retained recall datum", "retained prior-session datum", "selected-runtime"} {
				if !strings.Contains(message.ContentString(), want) {
					t.Fatalf("locally admitted enrichment omitted %q: %s", want, message.ContentString())
				}
			}
		}
	}
	if original.InternalID == "" {
		t.Fatal("original input identity was not published")
	}
	if stopped, _ := o.CancelTurn(); !stopped {
		t.Fatal("Stop did not cancel the exact active request")
	}
	waitTurnDone(t, sender, o.ID)
	path, sessionID := s.dbPath, s.sessionID
	s.Close()
	resumed, err := ResumeSessionWithDir(path, cfg, home, directory)
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()
	resumed.SetWorkDir(project)
	if calls.Load() != 1 {
		t.Fatalf("Stop/passive resume started replacement work: %d requests", calls.Load())
	}
	for _, ref := range refs {
		if err := sessiondb.DeletePromptReference(resumed.db, ref.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(imagePath); err != nil {
		t.Fatal(err)
	}
	sender = newMockEventSender()
	o, err = resumed.NewOrchestrator(sender)
	if err != nil {
		t.Fatal(err)
	}
	if !o.SubmitInput(provider.PlainInput("new explicit task")) {
		t.Fatal("new task was rejected after Stop/passive resume")
	}
	waitTurnDone(t, sender, o.ID)
	select {
	case second := <-requests:
		assertSelection(second)
	default:
		t.Fatal("new explicit request never reached the provider")
	}
	state, found, err = sessiondb.LoadContextState(resumed.db, sessionID, sessionID)
	if err != nil || !found {
		t.Fatalf("load resumed context: found=%v err=%v", found, err)
	}
	count := 0
	for _, message := range state.Messages {
		if message.InternalID == input.InternalID {
			count++
			if !reflect.DeepEqual(message, original) {
				t.Fatal("later execution mutated admitted original bytes")
			}
		}
	}
	if count != 1 || calls.Load() != 2 {
		t.Fatalf("retained original/request count: originals=%d calls=%d", count, calls.Load())
	}
}

func TestSelectedReferencePreparationFailurePreservesInputWithoutEffects(t *testing.T) {
	for _, failure := range []string{"missing", "inactive", "foreign", "unreadable", "unsupported-kind", "unsupported-model", "explicit-empty-model", "unsupported-protocol", "reference-load", "closed-memory", "unreadable-summaries"} {
		t.Run(failure, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"must not run\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			}))
			t.Cleanup(server.Close)
			home, directory, project := t.TempDir(), t.TempDir(), t.TempDir()
			cfg := mediaAttributionConfig(server.URL, "selected-reference-failure-"+failure, "openai", provider.ModalityImage)
			if failure == "unsupported-protocol" {
				cfg.Accounts["account"] = provider.Account{ID: "account", ProviderID: "custom", APIKey: "fixture", Connection: provider.Template{Name: "Local", ProviderURL: server.URL, APIStandard: "anthropic", AuthType: provider.AuthProtocol}}
				category := cfg.Agents.Profiles["default"].Categories[CategoryDefault]
				category.ModelModalities = []string{provider.ModalityAudio}
				cfg.Agents.Profiles["default"].Categories[CategoryDefault] = category
			}
			if failure == "unsupported-model" {
				category := cfg.Agents.Profiles["default"].Categories[CategoryDefault]
				provider.SetModelCap(category.Model, false, true)
				category.ModelModalities = nil
				cfg.Agents.Profiles["default"].Categories[CategoryDefault] = category
			}
			if failure == "explicit-empty-model" {
				category := cfg.Agents.Profiles["default"].Categories[CategoryDefault]
				category.ModelModalities = []string{}
				cfg.Agents.Profiles["default"].Categories[CategoryDefault] = category
			}
			s, err := NewWithSessionDir("selection-failure", cfg, home, directory)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(s.Close)
			s.SetWorkDir(project)
			if failure == "closed-memory" || failure == "unreadable-summaries" {
				store := newTestMemoryStore(t)
				s.SetMemoryStore(store)
				if failure == "closed-memory" {
					if err := store.DB().Close(); err != nil {
						t.Fatal(err)
					}
				} else if _, err := store.DB().Exec(`ALTER TABLE memory_session_summaries RENAME TO unavailable_summaries`); err != nil {
					t.Fatal(err)
				}
			}
			path := filepath.Join(project, "selected.png")
			if err := os.WriteFile(path, mediaPNG.content, 0o600); err != nil {
				t.Fatal(err)
			}
			ref := sessiondb.Reference{ID: "selected", SessionID: s.sessionID, Kind: sessiondb.KindPastedImage, Label: "[Selected]", FilePath: path, Mime: "image/png"}
			switch failure {
			case "inactive":
				ref.DeletedAt = time.Now().UnixNano()
			case "foreign":
				if err := sessiondb.CreateSessionRow(s.db, "foreign"); err != nil {
					t.Fatal(err)
				}
				ref.SessionID = "foreign"
			case "unreadable":
				ref.FilePath = project
			case "unsupported-kind":
				ref.Kind = "unknown-kind"
			case "unsupported-protocol":
				ref.Kind, ref.Mime, ref.FilePath = sessiondb.KindSelectedFile, "audio/wav", filepath.Join(project, "selected.wav")
				if err := os.WriteFile(ref.FilePath, mediaWAV.content, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if failure != "missing" {
				if err := sessiondb.InsertPromptReference(s.db, ref); err != nil {
					t.Fatal(err)
				}
			}
			if failure == "reference-load" {
				if _, err := s.db.Exec(`ALTER TABLE prompt_references RENAME TO unavailable_references`); err != nil {
					t.Fatal(err)
				}
			}
			sender := newMockEventSender()
			o, err := s.NewOrchestrator(sender)
			if err != nil {
				t.Fatal(err)
			}
			input := provider.InputWithRefs("submitted original [Selected]", []string{ref.ID})
			input.ContextParts = []string{"submitted preparation preimage"}
			intent, err := s.PrepareInputAdmission(o.ID, input, 5)
			if err != nil {
				t.Fatal(err)
			}
			input.InternalID = intent.InputID
			result := s.CommitInputAdmission(t.Context(), intent)
			if result.Receipt.InputID != input.InternalID {
				t.Fatalf("preparation failure rejected/discarded the submitted original: %+v", result)
			}
			var status recoverytypes.Status
			deadline := time.After(5 * time.Second)
		waitAttention:
			for {
				select {
				case event := <-sender.ch:
					if event.typ == "tool_call" || event.typ == "tool_result" || event.typ == "done" {
						t.Fatalf("failed preparation advanced execution: %s", event.typ)
					}
					if event.typ == "recovery" {
						status = event.data.(recoverytypes.Status)
						if status.State == recoverytypes.NeedsAttention {
							break waitAttention
						}
					}
				case <-deadline:
					t.Fatal("preparation failure did not surface NeedsAttention")
				}
			}
			if status.Detail == "" || status.Action == "" || calls.Load() != 0 {
				t.Fatalf("preparation was not actionable/fail-closed: %+v requests=%d", status, calls.Load())
			}
			if failure == "closed-memory" && !strings.Contains(status.Detail, "recall workspace memory") {
				t.Fatalf("closed memory did not fail at enabled recall: %+v", status)
			}
			if failure == "unreadable-summaries" && !strings.Contains(status.Detail, "load previous session summaries") {
				t.Fatalf("unreadable summaries did not fail at continuity preparation: %+v", status)
			}
			checkpoint, found, err := sessiondb.LoadRecoveryCheckpoint(s.db, s.sessionID, o.ID)
			if err != nil || !found || !checkpoint.InputAdmitted || !reflect.DeepEqual(checkpoint.PendingInput, input) {
				t.Fatalf("submitted original/selection preimage was not retained: %+v found=%v err=%v", checkpoint, found, err)
			}
			var captured provider.ChatMessage
			for _, message := range checkpoint.ActiveContext {
				if message.InternalID == input.InternalID+"-references" {
					captured = message
				}
			}
			switch failure {
			case "unsupported-model", "explicit-empty-model", "unsupported-protocol", "closed-memory", "unreadable-summaries":
				if captured.InternalID == "" || captured.EffectiveVisibility() != provider.VisibilityContextOnly {
					t.Fatal("later preparation failure discarded the captured selected preimage")
				}
			}
			if s.CancelRecoveryFor(o.ID, status.ID, status.Generation) != nil {
				t.Fatal("Stop could not reach the blocked preparation")
			}
			if failure == "reference-load" {
				if _, err := s.db.Exec(`ALTER TABLE unavailable_references RENAME TO prompt_references`); err != nil {
					t.Fatal(err)
				}
			}
			if failure == "unreadable-summaries" {
				if _, err := s.MemoryStore().DB().Exec(`ALTER TABLE unavailable_summaries RENAME TO memory_session_summaries`); err != nil {
					t.Fatal(err)
				}
			}
			path = s.dbPath
			s.Close()
			resumed, err := ResumeSessionWithDir(path, cfg, home, directory)
			if err != nil {
				t.Fatal(err)
			}
			defer resumed.Close()
			state, found, err := sessiondb.LoadContextState(resumed.db, resumed.sessionID, resumed.sessionID)
			if err != nil || !found {
				t.Fatalf("load stopped/resumed input: found=%v err=%v", found, err)
			}
			count, capturedCount := 0, 0
			for _, message := range state.Messages {
				if message.InternalID == input.InternalID {
					count++
					if message.ContentString() != input.Text {
						t.Fatalf("Stop/resume changed the submitted original: %+v", message)
					}
				}
				if captured.InternalID != "" && message.InternalID == captured.InternalID {
					capturedCount++
					if !reflect.DeepEqual(message, captured) {
						t.Fatal("Stop/passive resume changed the failed selected preimage")
					}
				}
			}
			if count != 1 || (captured.InternalID != "" && capturedCount != 1) || calls.Load() != 0 {
				t.Fatalf("Stop/resume lost original/selection or ran work: originals=%d selections=%d requests=%d", count, capturedCount, calls.Load())
			}
		})
	}
}

func TestSelectedReferenceCapabilityRepairReusesCapturedPreimage(t *testing.T) {
	const model = "selected-known-media-repair"
	capture := newRequestCapture(t, model, "data: {\"choices\":[{\"delta\":{\"content\":\"repaired request completed\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	provider.SetModelCap(model, false, true)
	cfg := modelAttributionConfig(capture.server.URL, model)
	s, err := NewWithSessionDir("selected-media-repair", cfg, t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	project := t.TempDir()
	s.SetWorkDir(project)
	path := filepath.Join(project, mediaPNG.name)
	if err := os.WriteFile(path, mediaPNG.content, 0o600); err != nil {
		t.Fatal(err)
	}
	ref := sessiondb.Reference{ID: "selected-repair-media", SessionID: s.sessionID, Kind: sessiondb.KindPastedImage, Label: "[Selected image]", FilePath: path, Mime: "image/png"}
	if err := sessiondb.InsertPromptReference(s.db, ref); err != nil {
		t.Fatal(err)
	}
	sender := newMockEventSender()
	o, err := s.NewOrchestrator(sender)
	if err != nil {
		t.Fatal(err)
	}
	input := provider.InputWithRefs("submitted original [Selected image]", []string{ref.ID, ref.ID})
	input.ContextParts = []string{"exact repair input ContextParts"}
	intent, err := s.PrepareInputAdmission(o.ID, input, 6)
	if err != nil {
		t.Fatal(err)
	}
	input.InternalID = intent.InputID
	if result := s.CommitInputAdmission(t.Context(), intent); !result.Accepted() {
		t.Fatalf("failed preparation did not preserve the locally admitted original: %+v", result)
	}
	awaitAttention := func(after time.Time) recoverytypes.Status {
		t.Helper()
		deadline := time.After(5 * time.Second)
		for {
			select {
			case event := <-sender.ch:
				if event.typ == "done" || event.typ == "tool_call" || event.typ == "tool_result" {
					t.Fatalf("unrepaired selected media advanced execution: %s", event.typ)
				}
				if event.typ == "recovery" {
					status := event.data.(recoverytypes.Status)
					if status.State == recoverytypes.NeedsAttention && status.UpdatedAt.After(after) {
						return status
					}
				}
			case <-deadline:
				t.Fatal("unsupported selected media did not surface a fresh NeedsAttention")
			}
		}
	}
	status := awaitAttention(time.Time{})
	if status.Action == "" || !strings.Contains(status.Detail, "image") {
		t.Fatalf("selected media failure was not actionable: %+v", status)
	}
	// PublishContext registers the complete immutable identities, including the
	// captured bytes which must survive later reference/file changes.
	identities := make(map[string]string)
	for _, id := range []string{input.InternalID, input.InternalID + "-references"} {
		var raw string
		if err := s.db.QueryRow(`SELECT message_json FROM immutable_message_identities
			WHERE session_id=? AND agent_id=? AND internal_id=?`, s.sessionID, s.sessionID, id).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		identities[id] = raw
	}
	before := s.MessagesWithMeta()
	records := make(map[string]sessiondb.MessageWithMeta, len(identities))
	for _, record := range before {
		if _, retained := identities[record.Msg.InternalID]; retained {
			if record.CreatedAt == 0 {
				t.Fatalf("admission did not timestamp input-owned record %q", record.Msg.InternalID)
			}
			records[record.Msg.InternalID] = record
		}
	}
	if len(records) != len(identities) {
		t.Fatal("partial preparation was not retained at admission")
	}
	checkpoint, found, err := sessiondb.LoadRecoveryCheckpoint(s.db, s.sessionID, o.ID)
	if err != nil || !found || !reflect.DeepEqual(checkpoint.PendingInput, input) {
		t.Fatalf("failure lost exact original/selection/context parts: found=%v err=%v checkpoint=%+v", found, err, checkpoint)
	}
	changed := append([]byte(nil), mediaPNG.content...)
	changed[len(changed)-1] ^= 1
	if err := os.WriteFile(path, changed, 0o600); err != nil {
		t.Fatal(err)
	}
	if s.RetryRecoveryFor(o.ID, status.ID, status.Generation) != nil {
		t.Fatal("exact unrepaired Continue was rejected")
	}
	status = awaitAttention(status.UpdatedAt)
	capture.mu.Lock()
	requestCount := len(capture.requests)
	capture.mu.Unlock()
	if requestCount != 0 {
		t.Fatalf("unrepaired Continue issued %d weakened requests", requestCount)
	}
	repaired := copyConfig(cfg)
	category := repaired.Agents.Profiles["default"].Categories[CategoryDefault]
	category.ModelModalities = []string{provider.ModalityImage}
	repaired.Agents.Profiles["default"].Categories[CategoryDefault] = category
	s.runtimeProfile.UpdateBase(repaired)
	if s.RetryRecoveryFor(o.ID, status.ID, status.Generation) != nil {
		t.Fatal("exact Continue after capability repair was rejected")
	}
	deadline := time.After(5 * time.Second)
waitCompletion:
	for {
		select {
		case event := <-sender.ch:
			if event.typ == "done" {
				break waitCompletion
			}
			if event.typ == "recovery" {
				recovery := event.data.(recoverytypes.Status)
				if recovery.State == recoverytypes.NeedsAttention && recovery.UpdatedAt.After(status.UpdatedAt) {
					t.Fatalf("explicit repair returned to NeedsAttention: %+v", recovery)
				}
			}
		case <-deadline:
			t.Fatal("explicit repair did not complete the original input")
		}
	}
	capture.mu.Lock()
	defer capture.mu.Unlock()
	if len(capture.requests) != 1 {
		t.Fatalf("repaired request count = %d", len(capture.requests))
	}
	var text strings.Builder
	images := 0
	for _, part := range capture.requests[0].Parts {
		if value, ok := part["text"].(string); ok {
			text.WriteString(value)
			text.WriteByte('\n')
		}
		if part["type"] == "image_url" {
			image, _ := part["image_url"].(map[string]any)
			if image["url"] != "data:image/png;base64,"+base64.StdEncoding.EncodeToString(mediaPNG.content) {
				t.Fatalf("Continue adopted changed backing bytes instead of the captured preimage: %+v", image)
			}
			images++
		}
	}
	for _, expected := range []string{input.Text, input.ContextParts[0], ref.AgentText()} {
		if strings.Count(text.String(), expected) != 1 {
			t.Fatalf("repaired request did not contain source exactly once: %q", expected)
		}
	}
	if images != 1 {
		t.Fatalf("repaired request contains %d selected images", images)
	}
	for id, original := range identities {
		var raw string
		if err := s.db.QueryRow(`SELECT message_json FROM immutable_message_identities
			WHERE session_id=? AND agent_id=? AND internal_id=?`, s.sessionID, s.sessionID, id).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		if raw != original {
			t.Fatalf("repair changed immutable admitted bytes for %q", id)
		}
	}
	after := s.MessagesWithMeta()
	counts := make(map[string]int, len(identities))
	for _, record := range after {
		if original, retained := records[record.Msg.InternalID]; retained {
			counts[record.Msg.InternalID]++
			if !reflect.DeepEqual(record, original) {
				t.Fatalf("repair restamped or rewrote admitted record %q: before=%+v after=%+v", record.Msg.InternalID, original, record)
			}
		}
	}
	for id := range records {
		if counts[id] != 1 {
			t.Fatalf("repair retained %d copies of admitted record %q", counts[id], id)
		}
	}
}
