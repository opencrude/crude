package session

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"crude/agent"
	"crude/provider"
	"crude/sessiondb"
)

func TestAutomaticCompactionCommitsLiveReplayTailAndContinues(t *testing.T) {
	var extractions, continuations atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request provider.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		if request.Model == "adaptive-aeron-model" {
			extractions.Add(1)
			writeAdaptiveCompactionSSE(t, w, "<<<intent>>>\nPreserve requirements.\n<<<work>>>\nOlder evidence reviewed.\n<<<state>>>\nNo files changed.\n<<<continuation>>>\nContinue the pending request.")
			return
		}
		continuations.Add(1)
		body, _ := json.Marshal(request.Messages)
		if !bytes.Contains(body, []byte("context_checkpoint")) || !bytes.Contains(body, []byte("pending-requirement")) {
			t.Error("continuation omitted the checkpoint or retained pending request")
		}
		writeAdaptiveCompactionSSE(t, w, "continued after checkpoint")
	}))
	defer server.Close()
	var history []provider.ChatMessage
	for turn := 0; turn < 5; turn++ {
		history = append(history,
			provider.NewTextMessage("user", fmt.Sprintf("requirement %d", turn)),
			provider.NewTextMessage("assistant", strings.Repeat("older evidence ", 6000)),
		)
	}
	live := provider.NewTextMessage("assistant", "recent evidence")
	live.ProviderItems = []provider.ProviderItem{{
		Standard: "openai-responses", Type: "reasoning",
		Data: json.RawMessage(`{ "type": "reasoning", "encrypted_content": "opaque<&>signature", "summary": [] }`),
	}}
	history = append(history, provider.NewTextMessage("user", "recent requirement"), live, provider.NewTextMessage("user", "pending-requirement"))
	for i := range history {
		history[i].InternalID = fmt.Sprintf("source-%d", i)
		history[i] = canonicalContextMessage(history[i])
	}
	s, dbPath := newAdaptiveCompactionSession(t, server.URL, 200_000, history)
	var deltas []sessiondb.ContextMessageDelta
	for i, message := range history {
		deltas = append(deltas, sessiondb.ContextMessageDelta{Message: message, CreatedAt: int64(i + 1), Turn: i/2 + 1, AgentID: s.sessionID})
	}
	state, err := sessiondb.PublishContext(s.db, sessiondb.ContextPublication{
		SessionID: s.sessionID, AgentID: s.sessionID, ExpectedRevision: 1, TranscriptAppend: deltas,
	})
	if err != nil {
		t.Fatal(err)
	}
	s.contextRevision = state.Revision
	before, err := sessiondb.LoadMessagesWithMeta(s.db)
	if err != nil {
		t.Fatal(err)
	}
	p := provider.FromPreset("openai-chat", "key", server.URL)
	p.Model = "replay-tail-main"
	provider.SetModelContext(p.Model, 200_000)
	a := agent.NewAgent(p, p.Model, s.logger)
	a.MaxOutputTokens, a.ContextCompactionPercent = 2048, 20
	a.CompactionProvider, err = s.cfg.providerForCategory(s.cfg.GetCategoryConfig(CategoryAeron))
	if err != nil {
		t.Fatal(err)
	}
	a.CompactionMaxOutput = 2048
	var revision uint64
	a.PrepareCompaction = func(source agent.CompactionSource) error {
		var err error
		revision, err = s.beginRootCompaction(s.sessionID, source)
		return err
	}
	a.CommitCompaction = func(commit agent.CompactionCommit) error {
		return s.commitRootCompaction(s.sessionID, revision, commit)
	}
	var compacted, failures int
	result := a.Run(t.Context(), history, "system", func(event agent.AgentEvent) {
		switch event.Type {
		case "context_compacted":
			compacted++
		case "context_compaction_failed":
			failures++
		}
	}, nil, s.sessionID, nil)
	if a.RunFailure() != nil || compacted != 1 || failures != 0 || extractions.Load() != 1 || continuations.Load() != 1 {
		t.Fatalf("automatic compaction did not continue: failure=%v commits=%d failures=%d extractions=%d continuations=%d", a.RunFailure(), compacted, failures, extractions.Load(), continuations.Load())
	}
	if len(result) == 0 || result[len(result)-1].ContentString() != "continued after checkpoint" {
		t.Fatal("run did not complete its normal response after compaction")
	}
	if s.contextGeneration != 8 {
		t.Fatalf("compaction generation=%d, want 8", s.contextGeneration)
	}
	found := false
	for _, message := range s.activeContext {
		if message.InternalID == history[len(history)-2].InternalID {
			found = true
			if string(message.ProviderItems[0].Data) != string(live.ProviderItems[0].Data) {
				t.Fatal("compaction rewrote the live replay bytes")
			}
		}
	}
	if !found {
		t.Fatal("the recent provider-replay message was not retained")
	}
	after, err := sessiondb.LoadMessagesWithMeta(s.db)
	if err != nil || !bytes.Equal(adaptiveRowsBytes(t, before), adaptiveRowsBytes(t, after)) {
		t.Fatal("compaction changed the immutable transcript")
	}
	activeBytes := adaptiveMessageBytes(t, s.activeContext)
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := sessiondb.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	resumed, exists, err := sessiondb.LoadContextState(reopened, s.sessionID, s.sessionID)
	if err != nil || !exists || resumed.Generation != 8 || !bytes.Equal(activeBytes, adaptiveMessageBytes(t, resumed.Messages)) || continuations.Load() != 1 {
		t.Fatalf("passive resume changed context or replayed a request: exists=%t err=%v", exists, err)
	}
}

func adaptiveRowsBytes(t *testing.T, rows []sessiondb.MessageWithMeta) []byte {
	t.Helper()
	data, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
